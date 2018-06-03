# Copyright (c) 2015-2016 Tigera, Inc. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
import datetime
import functools
import json
import logging
import os
import pdb
import re
import socket
import sys
from subprocess import CalledProcessError
from subprocess import check_output, STDOUT
from time import sleep

import termios
import yaml
from netaddr import IPNetwork, IPAddress
from exceptions import CommandExecError

LOCAL_IP_ENV = "MY_IP"
LOCAL_IPv6_ENV = "MY_IPv6"
logger = logging.getLogger(__name__)

ETCD_SCHEME = os.environ.get("ETCD_SCHEME", "http")
ETCD_CA = os.environ.get("ETCD_CA_CERT_FILE", "")
ETCD_CERT = os.environ.get("ETCD_CERT_FILE", "")
ETCD_KEY = os.environ.get("ETCD_KEY_FILE", "")
ETCD_HOSTNAME_SSL = "etcd-authority-ssl"

"""
Compile Regexes
"""
# Splits into groups that start w/ no whitespace and contain all lines below
# that start w/ whitespace
INTERFACE_SPLIT_RE = re.compile(r'(\d+:.*(?:\n\s+.*)+)')
# Grabs interface name
IFACE_RE = re.compile(r'^\d+: (\S+):')
# Grabs v4 addresses
IPV4_RE = re.compile(r'inet ((?:\d+\.){3}\d+)/\d+')
# Grabs v6 addresses
IPV6_RE = re.compile(r'inet6 ([a-fA-F\d:]+)/\d{1,3}')


def get_ip(v6=False):
    """
    Return a string of the IP of the hosts interface.
    Try to get the local IP from the environment variables.  This allows
    testers to specify the IP address in cases where there is more than one
    configured IP address for the test system.
    """
    env = LOCAL_IPv6_ENV if v6 else LOCAL_IP_ENV
    ip = os.environ.get(env)
    if not ip:
        try:
            logger.debug("%s not set; try to auto detect IP.", env)
            socket_type = socket.AF_INET6 if v6 else socket.AF_INET
            s = socket.socket(socket_type, socket.SOCK_DGRAM)
            remote_ip = "2001:4860:4860::8888" if v6 else "8.8.8.8"
            s.connect((remote_ip, 0))
            ip = s.getsockname()[0]
            s.close()
        except BaseException:
            # Failed to connect, just try to get the address from the interfaces
            version = 6 if v6 else 4
            ips = get_host_ips(version)
            if ips:
                ip = str(ips[0])
    else:
        logger.debug("Got local IP from %s=%s", env, ip)
    return ip


# Some of the commands we execute like to mess with the TTY configuration, which can break the
# output formatting. As a wrokaround, save off the terminal settings and restore them after
# each command.
_term_settings = termios.tcgetattr(sys.stdin.fileno())


def log_and_run(command, raise_exception_on_failure=True):
    def log_output(results):
        if results is None:
            logger.info("  # <no output>")

        lines = results.split("\n")
        for line in lines:
            logger.info("  # %s", line.rstrip())

    try:
        logger.info("[%s] %s", datetime.datetime.now(), command)
        try:
            results = check_output(command, shell=True, stderr=STDOUT).rstrip()
        finally:
            # Restore terminal settings in case the command we ran manipulated them.  Note:
            # under concurrent access, this is still not a perfect solution since another thread's
            # child process may break the settings again before we log below.
            termios.tcsetattr(sys.stdin.fileno(), termios.TCSADRAIN, _term_settings)
        log_output(results)
        return results
    except CalledProcessError as e:
        # Wrap the original exception with one that gives a better error
        # message (including command output).
        logger.info("  # Return code: %s", e.returncode)
        log_output(e.output)
        if raise_exception_on_failure:
            raise CommandExecError(e)


def retry_until_success(function, retries=10, ex_class=Exception, *args, **kwargs):
    """
    Retries function until no exception is thrown. If exception continues,
    it is reraised.

    :param function: the function to be repeatedly called
    :param retries: the maximum number of times to retry the function.
    A value of 0 will run the function once with no retries.
    :param ex_class: The class of expected exceptions.
    :returns: the value returned by function
    """
    for retry in range(retries + 1):
        try:
            result = function(*args, **kwargs)
        except ex_class:
            if retry < retries:
                sleep(1)
            else:
                raise
        else:
            # Successfully ran the function
            return result


def debug_failures(fn):
    """
    Decorator function to decorate assertion methods to pause the live system
    when an assertion fails, allowing the user to debug the problem.
    :param fn: The function to decorate.
    :return: The decorated function.
    """

    def wrapped(*args, **kwargs):
        try:
            return fn(*args, **kwargs)
        except KeyboardInterrupt:
            raise
        except Exception as e:
            if (os.getenv("DEBUG_FAILURES") is not None and
                    os.getenv("DEBUG_FAILURES").lower() == "true"):
                logger.error("TEST FAILED:\n%s\nEntering DEBUG mode."
                             % e.message)
                pdb.set_trace()
            else:
                raise

    return wrapped


@debug_failures
def check_bird_status(host, expected):
    """
    Check the BIRD status on a particular host to see if it contains the
    expected BGP status.

    :param host: The host object to check.
    :param expected: A list of tuples containing:
        (peertype, ip address, state)
    where 'peertype' is one of "Global", "Mesh", "Node",  'ip address' is
    the IP address of the peer, and state is the expected BGP state (e.g.
    "Established" or "Idle").
    """
    output = host.calicoctl("node status")
    lines = output.split("\n")
    for (peertype, ipaddr, state) in expected:
        for line in lines:
            # Status table format is of the form:
            # +--------------+-------------------+-------+----------+-------------+
            # | Peer address |     Peer type     | State |  Since   |     Info    |
            # +--------------+-------------------+-------+----------+-------------+
            # | 172.17.42.21 | node-to-node mesh |   up  | 16:17:25 | Established |
            # | 10.20.30.40  |       global      | start | 16:28:38 |   Connect   |
            # |  192.10.0.0  |   node specific   | start | 16:28:57 |   Connect   |
            # +--------------+-------------------+-------+----------+-------------+
            #
            # Splitting based on | separators results in an array of the
            # form:
            # ['', 'Peer address', 'Peer type', 'State', 'Since', 'Info', '']
            columns = re.split("\s*\|\s*", line.strip())
            if len(columns) != 7:
                continue

            if type(state) is not list:
                state = [state]

            # Find the entry matching this peer.
            if columns[1] == ipaddr and columns[2] == peertype:

                # Check that the connection state is as expected.  We check
                # that the state starts with the expected value since there
                # may be additional diagnostic information included in the
                # info field.
                if any(columns[5].startswith(s) for s in state):
                    break
                else:
                    msg = "Error in BIRD status for peer %s:\n" \
                          "Expected: %s; Actual: %s\n" \
                          "Output:\n%s" % (ipaddr, state, columns[5],
                                           output)
                    raise AssertionError(msg)
        else:
            msg = "Error in BIRD status for peer %s:\n" \
                  "Type: %s\n" \
                  "Expected: %s\n" \
                  "Output: \n%s" % (ipaddr, peertype, state, output)
            raise AssertionError(msg)

@debug_failures
def update_bgp_config(host, nodeMesh=None, asNum=None):
    response = host.calicoctl("get BGPConfiguration -o yaml")
    bgpcfg = yaml.safe_load(response)

    if len(bgpcfg['items']) == 0:
        bgpcfg = {
            'apiVersion': 'projectcalico.org/v3',
            'kind': 'BGPConfigurationList',
            'items': [ {
                    'apiVersion': 'projectcalico.org/v3',
                    'kind': 'BGPConfiguration',
                    'metadata': { 'name': 'default', },
                    'spec': {}
                }
            ]
        }

    if 'creationTimestamp' in bgpcfg['items'][0]['metadata']:
        del bgpcfg['items'][0]['metadata']['creationTimestamp']

    if nodeMesh is not None:
        bgpcfg['items'][0]['spec']['nodeToNodeMeshEnabled'] = nodeMesh

    if asNum is not None:
        bgpcfg['items'][0]['spec']['asNumber'] = asNum

    host.writejson("bgpconfig", bgpcfg)
    host.calicoctl("apply -f bgpconfig")
    host.execute("rm -f bgpconfig")

@debug_failures
def get_bgp_spec(host):
    response = host.calicoctl("get BGPConfiguration -o yaml")
    bgpcfg = yaml.safe_load(response)

    return bgpcfg['items'][0]['spec']

@debug_failures
def assert_number_endpoints(host, expected):
    """
    Check that a host has the expected number of endpoints in Calico
    Parses the "calicoctl endpoint show" command for number of endpoints.
    Raises AssertionError if the number of endpoints does not match the
    expected value.

    :param host: DockerHost object
    :param expected: int, number of expected endpoints
    :return: None
    """
    hostname = host.get_hostname()
    out = host.calicoctl("get workloadEndpoint -o yaml")
    output = yaml.safe_load(out)
    actual = 0
    for endpoint in output['items']:
        if endpoint['spec']['node'] == hostname:
            actual += 1

    if int(actual) != int(expected):
        raise AssertionError(
            "Incorrect number of endpoints on host %s: \n"
            "Expected: %s; Actual: %s" % (hostname, expected, actual)
        )

@debug_failures
def apply_cnx_license(host):
    """
    Apply a Tigera CNX license key

    :param host: DockerHost object to run calicoctl on
    :return: None
    """
    cnx_license = {
        'apiVersion': 'projectcalico.org/v3',
        'kind': 'LicenseKey',
        'metadata': { 'name': 'default', },
        'spec': {
            'certificate': """"
-----BEGIN CERTIFICATE-----
MIIFxjCCA66gAwIBAgIQVq3rz5D4nQF1fIgMEh71DzANBgkqhkiG9w0BAQsFADCB
tTELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWExFjAUBgNVBAcTDVNh
biBGcmFuY2lzY28xFDASBgNVBAoTC1RpZ2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1
cml0eSA8c2lydEB0aWdlcmEuaW8+MT8wPQYDVQQDEzZUaWdlcmEgRW50aXRsZW1l
bnRzIEludGVybWVkaWF0ZSBDZXJ0aWZpY2F0ZSBBdXRob3JpdHkwHhcNMTgwNDA1
MjEzMDI5WhcNMjAxMDA2MjEzMDI5WjCBnjELMAkGA1UEBhMCVVMxEzARBgNVBAgT
CkNhbGlmb3JuaWExFjAUBgNVBAcTDVNhbiBGcmFuY2lzY28xFDASBgNVBAoTC1Rp
Z2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1cml0eSA8c2lydEB0aWdlcmEuaW8+MSgw
JgYDVQQDEx9UaWdlcmEgRW50aXRsZW1lbnRzIENlcnRpZmljYXRlMIIBojANBgkq
hkiG9w0BAQEFAAOCAY8AMIIBigKCAYEAwg3LkeHTwMi651af/HEXi1tpM4K0LVqb
5oUxX5b5jjgi+LHMPzMI6oU+NoGPHNqirhAQqK/k7W7r0oaMe1APWzaCAZpHiMxE
MlsAXmLVUrKg/g+hgrqeije3JDQutnN9h5oZnsg1IneBArnE/AKIHH8XE79yMG49
LaKpPGhpF8NoG2yoWFp2ekihSohvqKxa3m6pxoBVdwNxN0AfWxb60p2SF0lOi6B3
hgK6+ILy08ZqXefiUs+GC1Af4qI1jRhPkjv3qv+H1aQVrq6BqKFXwWIlXCXF57CR
hvUaTOG3fGtlVyiPE4+wi7QDo0cU/+Gx4mNzvmc6lRjz1c5yKxdYvgwXajSBx2pw
kTP0iJxI64zv7u3BZEEII6ak9mgUU1CeGZ1KR2Xu80JiWHAYNOiUKCBYHNKDCUYl
RBErYcAWz2mBpkKyP6hbH16GjXHTTdq5xENmRDHabpHw5o+21LkWBY25EaxjwcZa
Y3qMIOllTZ2iRrXu7fSP6iDjtFCcE2bFAgMBAAGjZzBlMA4GA1UdDwEB/wQEAwIF
oDATBgNVHSUEDDAKBggrBgEFBQcDAjAdBgNVHQ4EFgQUIY7LzqNTzgyTBE5efHb5
kZ71BUEwHwYDVR0jBBgwFoAUxZA5kifzo4NniQfGKb+4wruTIFowDQYJKoZIhvcN
AQELBQADggIBAAK207LaqMrnphF6CFQnkMLbskSpDZsKfqqNB52poRvUrNVUOB1w
3dSEaBUjhFgUU6yzF+xnuH84XVbjD7qlM3YbdiKvJS9jrm71saCKMNc+b9HSeQAU
DGY7GPb7Y/LG0GKYawYJcPpvRCNnDLsSVn5N4J1foWAWnxuQ6k57ymWwcddibYHD
OPakOvO4beAnvax3+K5dqF0bh2Np79YolKdIgUVzf4KSBRN4ZE3AOKlBfiKUvWy6
nRGvu8O/8VaI0vGaOdXvWA5b61H0o5cm50A88tTm2LHxTXynE3AYriHxsWBbRpoM
oFnmDaQtGY67S6xGfQbwxrwCFd1l7rGsyBQ17cuusOvMNZEEWraLY/738yWKw3qX
U7KBxdPWPIPd6iDzVjcZrS8AehUEfNQ5yd26gDgW+rZYJoAFYv0vydMEyoI53xXs
cpY84qV37ZC8wYicugidg9cFtD+1E0nVgOLXPkHnmc7lIDHFiWQKfOieH+KoVCbb
zdFu3rhW31ygphRmgszkHwApllCTBBMOqMaBpS8eHCnetOITvyB4Kiu1/nKvVxhY
exit11KQv8F3kTIUQRm0qw00TSBjuQHKoG83yfimlQ8OazciT+aLpVaY8SOrrNnL
IJ8dHgTpF9WWHxx04DDzqrT7Xq99F9RzDzM7dSizGxIxonoWcBjiF6n5
-----END CERTIFICATE-----
""",
            'token': """eyJhbGciOiJBMTI4R0NNS1ciLCJjdHkiOiJKV1QiLCJlbmMiOiJBMTI4R0NNIiwiaXYiOiJlaWNWbHlTbGxFMlAtQ25tIiwidGFnIjoiTk1KSHlRV2M1UWZ6M1dydHNCamxhZyIsInR5cCI6IkpXVCJ9.afBv55v15cFsaHqcsyDkfA.yBMyDIRFBtWxyNxI.Q18a_G6i2kiN0NsqtGSQjc0o2CrkdivRJFkpAlkYIttBAultPADLZmfgf0nzVqZkKAkOGSbIxjY5BgW59FEyaiEs8sL11HZqPB8l2eOqK4BSj5wx3yEhsFzQkD1pZZz8qVgE0Ml3SaSiGVhe4ADTiSsUBbU9JD_aRaa4m1QvS4IQiT_QuWxUtOi-LRXsvHURnkTs3K_WGu7_QW5RRHDGD_CP2kfTUMeSvcWSiT8vgrgPj5q4Zpz4XTWNT-u0sJraWu79tOqCu9YwKeDVMKgJ04sunGc9xsimkhUmOnwuiIEeR24GyL7I5FDrCUC6Oiif62o_ECaQA6NjHAFdq-LNCIb902tKD0BQ-q6AzUrjs21GNr9_oJZJXKL6m74UJULMVgxXZKze2IH9EXtQ0b2jHbi9-qyMp6Rc34Z4HtYmQPB3CRHjDTmzUpEXOsF-reYffRHLJY5DUk7fDfTnhBmUksYonuuGLKep1_YYAiAhkomj7mupFNVN5JnZx8P-v4cfAr4PZxF6Lw5utN5R1hArroYA1Z-2Et0LC6BbE6Q1j7_zmaBs2BEnNfWNn2LFBBOCHzax51ISz_DIcGSidsRDNE9vQDYhcb9MGqOtaCDAA5zHCArVxu2PiwJj6JNbdNB9nvLWlAqxUU4zJwNPFd9xQIR53RFNB0LHID-ab_H7_NFX0auolwSz5Fm14ID4SKvD7_1aqUJG9_WiEtNz9yZJL5vkspdSxnR59L4alUYErxSEWGmOIBvJPemftZBilH1Vmxt0MFyu7sxK_uEJ55OtxNXCfaa_MPp0Yhn9mjTeCSMH8dV6ahZuL8B85BHjFkqY_nLV5UKEvPcyflo4JLDAOvhTZ0bbqvheEx48FQPisSJoK5zY61FqK1tFrID5rdJQ4RMpe4Bix0Dy213hN08U1iNklHUgR-MMw2f4sfGouBm-3B-7P9bqwQlEVyKLkyBzOgWd0PADc0i5bdxCxoqL8AAehPTEGIk-lb2TKe71dCW47oZQwigRgbLHRJnYF9iVlFoXXf-MLH_edh5Gi2OD397MtuBvpGWS8KVjiyUYX-NhvOqgzqrRCH-7kRkmYBsL446hNzGYMjbxut488a2amVrsIuR4oerJnkSdK3o.MnNW4M-g2iiXOi1GVe5zaQ""",
        }
    }

    host.writejson("cnxlicense", cnx_license)
    host.calicoctl("apply -f cnxlicense")
    host.execute("rm -f cnxlicense")

@debug_failures
def assert_profile(host, profile_name):
    """
    Check that profile is registered in Calico
    Parse "calicoctl profile show" for the given profilename

    :param host: DockerHost object
    :param profile_name: String of the name of the profile
    :return: Boolean: True if found, False if not found
    """
    out = host.calicoctl("get -o yaml profile")
    output = yaml.safe_load(out)
    found = False
    for profile in output['items']:
        if profile['metadata']['name'] == profile_name:
            found = True
            break

    if not found:
        raise AssertionError("Profile %s not found in Calico" % profile_name)


def get_profile_name(host, network):
    """
    Get the profile name from Docker
    A profile is created in Docker for each Network object.
    The profile name is a randomly generated string.

    :param host: DockerHost object
    :param network: Network object
    :return: String: profile name
    """
    info_raw = host.execute("docker network inspect %s" % network.name)
    info = json.loads(info_raw)

    # Network inspect returns a list of dicts for each network being inspected.
    # We are only inspecting 1, so use the first entry.
    return info[0]["Id"]


@debug_failures
def get_host_ips(version=4, exclude=None):
    """
    Gets all IP addresses assigned to this host.

    Ignores Loopback Addresses

    This function is fail-safe and will return an empty array instead of
    raising any exceptions.

    :param version: Desired IP address version. Can be 4 or 6. defaults to 4
    :param exclude: list of interface name regular expressions to ignore
                    (ex. ["^lo$","docker0.*"])
    :return: List of IPAddress objects.
    """
    exclude = exclude or []
    ip_addrs = []

    # Select Regex for IPv6 or IPv4.
    ip_re = IPV4_RE if version is 4 else IPV6_RE

    # Call `ip addr`.
    try:
        ip_addr_output = check_output(["ip", "-%d" % version, "addr"])
    except (CalledProcessError, OSError):
        print("Call to 'ip addr' Failed")
        sys.exit(1)

    # Separate interface blocks from ip addr output and iterate.
    for iface_block in INTERFACE_SPLIT_RE.findall(ip_addr_output):
        # Try to get the interface name from the block
        match = IFACE_RE.match(iface_block)
        iface = match.group(1)
        # Ignore the interface if it is explicitly excluded
        if match and not any(re.match(regex, iface) for regex in exclude):
            # Iterate through Addresses on interface.
            for address in ip_re.findall(iface_block):
                # Append non-loopback addresses.
                if not IPNetwork(address).ip.is_loopback():
                    ip_addrs.append(IPAddress(address))

    return ip_addrs

def curl_etcd(path, options=None, recursive=True, ip=None):
    """
    Perform a curl to etcd, returning JSON decoded response.
    :param path:  The key path to query
    :param options:  Additional options to include in the curl
    :param recursive:  Whether we want recursive query or not
    :return:  The JSON decoded response.
    """
    if options is None:
        options = []
    if ETCD_SCHEME == "https":
        # Etcd is running with SSL/TLS, require key/certificates
        rc = check_output(
            "curl --cacert %s --cert %s --key %s "
            "-sL https://%s:2379/v2/keys/%s?recursive=%s %s"
            % (ETCD_CA, ETCD_CERT, ETCD_KEY, ETCD_HOSTNAME_SSL,
               path, str(recursive).lower(), " ".join(options)),
            shell=True)
    else:
        rc = check_output(
            "curl -sL http://%s:2379/v2/keys/%s?recursive=%s %s"
            % (ip, path, str(recursive).lower(), " ".join(options)),
            shell=True)

    return json.loads(rc.strip())

def wipe_etcd(ip):
    # Delete /calico if it exists. This ensures each test has an empty data
    # store at start of day.
    curl_etcd("calico", options=["-XDELETE"], ip=ip)

    # Disable Usage Reporting to usage.projectcalico.org
    # We want to avoid polluting analytics data with unit test noise
    curl_etcd("calico/v1/config/UsageReportingEnabled",
                   options=["-XPUT -d value=False"], ip=ip)

    etcd_container_name = "calico-etcd"
    tls_vars = ""
    if ETCD_SCHEME == "https":
        # Etcd is running with SSL/TLS, require key/certificates
        etcd_container_name = "calico-etcd-ssl"
        tls_vars = ("ETCDCTL_CACERT=/etc/calico/certs/ca.pem " +
                    "ETCDCTL_CERT=/etc/calico/certs/client.pem " +
                    "ETCDCTL_KEY=/etc/calico/certs/client-key.pem ")

    check_output("docker exec " + etcd_container_name + " sh -c '" + tls_vars +
                 "ETCDCTL_API=3 etcdctl del --prefix /calico" +
                 "'", shell=True)


on_failure_fns = []


def clear_on_failures():
    global on_failure_fns
    on_failure_fns = []


def add_on_failure(fn):
    on_failure_fns.append(fn)


def handle_failure(fn):
    """
    Decorator for test methods so that, if they fail, they immediately print
    information about the problem and run any defined on_failure functions.
    :param fn: The function to decorate.
    :return: The decorated function.
    """
    @functools.wraps(fn)
    def wrapped(*args, **kwargs):
        try:
            return fn(*args, **kwargs)
        except KeyboardInterrupt:
            raise
        except Exception as e:
            logger.exception("TEST FAILED")
            for handler in on_failure_fns:
                logger.info("Calling failure fn %r", handler)
                handler()
            raise

    return wrapped


def dump_etcdv3():
    etcd_container_name = "calico-etcd"
    tls_vars = ""
    if ETCD_SCHEME == "https":
        # Etcd is running with SSL/TLS, require key/certificates
        etcd_container_name = "calico-etcd-ssl"
        tls_vars = ("ETCDCTL_CACERT=/etc/calico/certs/ca.pem " +
                    "ETCDCTL_CERT=/etc/calico/certs/client.pem " +
                    "ETCDCTL_KEY=/etc/calico/certs/client-key.pem ")

    log_and_run("docker exec " + etcd_container_name + " sh -c '" + tls_vars +
                 "ETCDCTL_API=3 etcdctl get --prefix /calico" +
                 "'")

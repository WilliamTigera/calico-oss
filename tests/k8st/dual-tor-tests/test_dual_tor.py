# Copyright (c) 2019 Tigera, Inc. All rights reserved.

import os
import re
import subprocess
import sys
import shlex
import threading
import time
import logging

from kubernetes import client, config

from tests.k8st.test_base import TestBase
from tests.k8st.utils.utils import retry_until_success, DiagsCollector, kubectl, run

_log = logging.getLogger(__name__)


def output_reader(proc, log):
    while True:
        line = proc.stdout.readline()
        if line:
            log.logs.append(line)
        else:
            break


class Log:
    def __init__(self):
        self.logs = []


def run_with_log(cmd, log):
    _log.info("run: %s", cmd)
    proc1 = subprocess.Popen(shlex.split(cmd), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    t1 = threading.Thread(target=output_reader, args=(proc1, log))
    t1.start()


class Flow(object):
    def __init__(self, client_pod, server_pod, target_ip, target_port, target_ip_short, target_port_short):
        # A Flow object represents a single connection from a client pod to a target IP
        # and port.  The target IP and port will always resolve to a server pod; sometimes
        # directly (pod IP), sometimes via a cluster IP and sometimes via a NodePort.
        # `server_pod` is the name of the expected server pod; we use this for logging and
        # for calculating the expected path back from the server to the client.
        # `server_ip` is the pod IP of the expected server pod; we use this for
        # calculating the expected path back from the client to the server.
        self.client_pod = client_pod
        self.server_pod = server_pod
        self.target_ip = target_ip
        self.target_port = target_port
        self.client_ip = get_pod_ip("pod-name", self.client_pod)
        self.server_ip = get_pod_ip("pod-name", self.server_pod)

        # IP and port for short-lived connections.
        self.target_ip_short = target_ip_short
        self.target_port_short = target_port_short


def get_pod_ip(key, value):
    cmd="kubectl get pods -n dualtor --selector=\"" + key + "=" + value + "\"" + " -o json 2> /dev/null " + " | jq -r '.items[] | \"\(.metadata.name) \(.status.podIP)\"'"
    output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
    s=output.split()
    if len(s) != 2:
        _log.exception("failed to get pod ip for label %s==%s", key, value)
        raise Exception("error getting pod ip")
    return s[1]


def get_service_ip(name):
    cmd = "kubectl get service " + name + " -n dualtor -o json | jq -r '.spec.clusterIP'"
    output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
    return output.strip()


def get_node_port(name):
    cmd="kubectl get service " + name + " -n dualtor -o json 2> /dev/null | jq -r '.spec.ports[] | \"\(.nodePort)\"'"
    output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
    return output.strip()


def get_dev(output):
    # output should only be 1 or 2
    dev = ""
    if output == "1":
        dev = "eth0"
    elif output == "2":
        dev = "eth1"
    else:
        _log.exception("failed to get interface name from plane output %s", output)
        raise Exception("error getting dev")

    return dev


def traceroute(src_pod_name, dst_ip, timeout):
    cmd = "kubectl exec " + src_pod_name + " -n dualtor -- timeout " + timeout + " traceroute -n " + dst_ip
    _log.info("run: %s", cmd)
    try:
        output = subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("output:\n%s", output)
    except subprocess.CalledProcessError as e:
        _log.info("rc %s output:\n%s", e.returncode, e.output)
        run("kubectl get po -A -o wide")
        run("kubectl describe po " + src_pod_name + " -n dualtor")
        raise
    return output.splitlines()


def get_plane(src_pod_name, dst_ip):

    # Normally, starting from a pod, we're interested in the second traceroute hop.
    route_order = 2
    if src_pod_name.endswith("-host"):
        # But from a host-networked pod it's the first hop.
        route_order = 1

    # traceroute src --> dst
    try:
        trlines = traceroute(src_pod_name, dst_ip, "5s")
    except Exception:
        print "For some reason, running ServiceIP failover test (without tor2tor, tor2node), traceroute could get into the the state that it lost packets on last test case. Print log for now and retry with longer timeout. We will debug it later."
        trlines = traceroute(src_pod_name, dst_ip, "25s")

    marker = str(route_order) + "  172"
    for l in trlines:
        if marker in l:
            m = re.search('172\.31\..(\d)', l)
            if m:
                return m.group(1)

    raise Exception("error match route info")


def get_calico_node_pod_for_node(node_name):
    pod_name = kubectl(
        "get po -n calico-system" +
        " -l k8s-app=calico-node" +
        " --field-selector spec.nodeName=" + node_name +
        " -o jsonpath='{.items[*].metadata.name}'")
    if not pod_name:
        raise Exception('pod name not found')
    return pod_name


def delete_calico_node_pod_for_node(node_name):
    pod_name = get_calico_node_pod_for_node(node_name)
    # In this rig, the calico-node DaemonSet is modified so that it only matches nodes
    # with label 'ctd: f'.  (That stands for "Calico Test Disabled: False".  In other
    # words, calico-node only runs on nodes where it is *not* disabled.)
    #
    # Therefore we can kill the calico-node on a node, and prevent it from restarting, by
    # changing the 'ctd' label to 't' (for True).
    kubectl("label --overwrite no %s ctd=t" % node_name)
    # Note: pod is automatically deleted because node no longer matches 'ctd: f' node selector.
    _log.info("Deleted calico-node pod on %s (%s)", node_name, pod_name)


def restart_calico_node_pod_for_node(node_name):
    # Change "Calico Test Disabled" label back to False.  Then the calico-node pod is
    # automatically restarted on this node.
    kubectl("label --overwrite no %s ctd=f" % node_name)
    _log.info("Labelled %s to allow calico-node to restart", node_name)


def get_early_logs(node_name):
    return run("docker exec %s podman logs calico-early" % node_name).splitlines()


class FailoverTestConfig(object):
    def __init__(self, total_packets, max_errors, flows):
        self.total_packets = total_packets
        self.max_errors = max_errors
        self.timeout = total_packets/100 + 10 # expect receiving 100 packets per seconds plus 10 seconds buffer.
        self.flows = flows

    def resolve_flows(self):
        for f in self.flows:
            # Calculate plane used from client towards server IP.
            f.client_plane = get_plane(f.client_pod, f.server_ip)

            # Calculate plane used from server towards client IP.
            f.server_plane = get_plane(f.server_pod, f.client_ip)

            _log.info("Testing flow: %s <%s> -> %s:%s -> %s <%s>", f.client_pod, f.client_ip, f.target_ip, f.target_port, f.server_pod, f.server_ip)
            _log.info("\tOutward path uses plane %s", f.client_plane)
            _log.info("\tReturn path uses plane %s", f.server_plane)

            if "rb" in f.server_pod:
                self.client_rb_plane = f.client_plane
                self.client_rb_dev = get_dev(f.client_plane)
                self.rb_server_plane = f.server_plane
                self.rb_server_dev = get_dev(f.server_plane)


class _FailoverTest(TestBase):
    @classmethod
    def setUpClass(cls):
        ensureCalicoReady()
        cluster = FailoverCluster()
        cluster.setup()

    @classmethod
    def tearDownClass(cls):
        cluster = FailoverCluster()
        cluster.cleanup()

    def setUp(self):
        super(_FailoverTest, self).setUp()

        # Before starting each test case, wait until all pod block routes are correctly
        # ECMP again, as they may need a little time to repair after being broken in the
        # previous test case.
        retry_until_success(self.routes_all_ecmp, retries=10, wait_time=6)

    def start_client(self, client_pod, ip, port):
        name = "from %s to %s:%s" % (client_pod, ip, port)
        script="for i in `seq 1 " + str(self.config.total_packets) + "`; do echo $i -- " + name + "; sleep 0.01; done | /reliable-nc " + ip + ":" + port
        cmd = "kubectl exec -n dualtor " + client_pod + " -- /bin/sh -c \"" + script + "\""
        _log.info("run: %s", cmd)
        proc1 = subprocess.Popen(shlex.split(cmd))

    def packets_received(self, name, server_log, count, previous_seq):
        error = 0

        if len(server_log) == 0:
            _log.exception("empty server log of %s at %d seconds", name, count)
            raise Exception("error empty server log")

        last_log = server_log[-1]
        if last_log.find("--") == -1:
            last_log = server_log[-2]
            if last_log.find("--") == -1:
                _log.exception("failed to parse server log of %s at %d seconds", name, count)
                raise Exception("error parsing server log")

        seq_string = last_log.split("--")[0]
        seq = int(seq_string)
        diff = seq - previous_seq

        _log.info("%d second -- %s %s packets received (latest seq # %d, %d server log lines)",
                  count, diff, name, seq, len(server_log))
        #check if packets received is more than 50 except for first and last iterations.
        if previous_seq != 0 and seq != self.config.total_packets and diff < 50:
            error =1
            _log.error("server log of %s at %d seconds -- received %d packets, link broken", name, count, diff)
            run("docker exec kind-worker ip r")
            run("docker exec kind-worker3 ip r")

        return seq, error

    def clean_up(self):
        names = ["ra-server", "rb-server"]
        for name in names:
            cmd_prefix="kubectl exec -n dualtor -t " + name + " -- "
            output=subprocess.check_output(cmd_prefix + "ps -a", shell=True, stderr=subprocess.STDOUT)
            if output.find("/reliable-nc 8090") != -1:
                subprocess.call(cmd_prefix + "killall reliable-nc", shell=True, stderr=subprocess.STDOUT)

    def routes_all_ecmp(self):
        _log.info("Check routing...")
        for node in ["kind-control-plane", "kind-worker", "kind-worker2", "kind-worker3"]:
            routes = run("docker exec %s ip r" % node)
            for line in routes.splitlines():
                if "/26 via" in line:
                    # This indicates a /26 route with a single path, which is wrong; for
                    # example: "10.244.195.192/26 via 172.31.12.1 dev eth1 proto bird".
                    # In comparison, a good route has "/26 proto bird": either a blackhole
                    # route on the node that hosts that /26, or an ECMP route with the
                    # possible paths on the following lines.
                    _log.info("Found non-ECMP /26 route: %s", line)
                    raise Exception("Non-ECMP /26 route on %s: %s", node, line)
        _log.info("All /26 routes are ECMP")

    def _run_single_test(self, case_name, break_func, restore_func):
        self.config.resolve_flows()

        for f in self.config.flows:
            f.server_log = Log()
            run_with_log("kubectl exec -n dualtor " + f.server_pod + " -- /reliable-nc 8090", f.server_log)
            f.previous_seq = 0
            f.errors = 0

        time.sleep(1)
        for f in self.config.flows:
            self.start_client(f.client_pod, f.target_ip, f.target_port)

        count = 0
        flows_still_running = len(self.config.flows)
        while flows_still_running > 0 and count < self.config.timeout:
            time.sleep(1)
            count += 1

            for f in self.config.flows:
                new_seq, error = self.packets_received(f.client_pod, f.server_log.logs, count, f.previous_seq)
                f.errors += error
                if new_seq > f.previous_seq and new_seq == self.config.total_packets:
                    flows_still_running -= 1
                f.previous_seq = new_seq

                if (count % 3) == 0:
                    # Test shortlived connection.
                    short_log = Log()
                    run_with_log("kubectl exec -n dualtor " + f.server_pod + " -- /reliable-nc 8091", short_log)
                    def short_connection():
                        run("kubectl exec -n dualtor " + f.client_pod + " -- /bin/sh -c 'echo hello | /reliable-nc " + f.target_ip_short + ":" + f.target_port_short + "'")
                    time.sleep(0.25)
                    retry_until_success(short_connection, retries=3, wait_time=0.25)
                    def check_transmission():
                        assert "hello\n" in short_log.logs, "Did not find 'hello' in server logs: %r" % short_log.logs
                    retry_until_success(check_transmission, retries=3, wait_time=0.25)

            if count == 5:
                break_func()

            if count == 15:
                restore_func()

        # cleanup servers
        self.clean_up()

        for f in self.config.flows:
            _log.info("%s: %s", f.server_pod, f.server_log.logs[-1].strip())

        for f in self.config.flows:
            if f.errors > self.config.max_errors:
                _log.exception("client to %s failover failed. error count %d.", f.server_pod, f.errors)
                raise Exception("failover test failed")

        _log.info("test completed.")

    def link_func_break_tor2tor(self):
        # break tor2tor link via eth1 of tor router.
        # client to rb-server is currently via client_rb_plane.
        cmd="docker exec bird-a" + self.config.client_rb_plane + " ip link set dev eth1 down"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: break tor2tor")

    def link_func_restore_tor2tor(self):
        cmd="docker exec bird-a" + self.config.client_rb_plane + " ip link set dev eth1 up"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: restore tor2tor")

    def link_func_break_tor2node(self):
        # break tor2node link via eth0 of tor router.
        # client to rb-server is currently via client_rb_plane.
        cmd="docker exec bird-b" + self.config.client_rb_plane + " ip link set dev eth0 down"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: break tor2node")

    def link_func_restore_tor2node(self):
        cmd="docker exec bird-b" + self.config.client_rb_plane + " ip link set dev eth0 up"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: restore tor2node")

    def link_func_drop_client_node(self):
        # drop packets silently on client node on interface to rb-server.
        cmd="docker exec kind-worker tc qdisc add dev " +  self.config.client_rb_dev + " root netem loss 100%"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: drop client node")

    def link_func_restore_client_node(self):
        cmd="docker exec kind-worker tc qdisc del dev " +  self.config.client_rb_dev + " root"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: restore client node")

    def link_func_drop_server_node(self):
        # drop packets silently on rb-server node on interface to client.
        cmd="docker exec kind-worker3 tc qdisc add dev " +  self.config.rb_server_dev + " root netem loss 100%"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: drop server node")

    def link_func_restore_server_node(self):
        cmd="docker exec kind-worker3 tc qdisc del dev " +  self.config.rb_server_dev + " root"
        output=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT)
        _log.info("link function: restore server node")

    def do_nothing(self):
        pass

    def test_failover_tor2tor(self):
        self._run_single_test("failover_tor2tor", self.link_func_break_tor2tor, self.link_func_restore_tor2tor)

    def test_failover_tor2node(self):
        self._run_single_test("failover_tor2node", self.link_func_break_tor2node, self.link_func_restore_tor2node)

    def test_failover_drop_client(self):
        self._run_single_test("failover_drop_client", self.link_func_drop_client_node, self.link_func_restore_client_node)

    def test_failover_drop_server(self):
        self._run_single_test("failover_drop_server", self.link_func_drop_server_node, self.link_func_restore_server_node)

    def test_basic_connection(self):
        self._run_single_test("basic_connection", self.do_nothing, self.do_nothing)

    # Test restarting calico-node for the client pod's node.
    def suppress_restart_calico_node_client(self):
        old_log_count = len(get_early_logs("kind-worker"))
        self._run_single_test(
            "restart_calico_node_client",
            lambda: delete_calico_node_pod_for_node("kind-worker"),
            lambda: restart_calico_node_pod_for_node("kind-worker"),
        )
        self.check_early_container_noticed_restart("kind-worker", old_log_count)

    # Test restarting calico-node for the ra-server pod's node.
    def suppress_restart_calico_node_ra_server(self):
        old_log_count = len(get_early_logs("kind-control-plane"))
        self._run_single_test(
            "restart_calico_node_ra_server",
            lambda: delete_calico_node_pod_for_node("kind-control-plane"),
            lambda: restart_calico_node_pod_for_node("kind-control-plane"),
        )
        self.check_early_container_noticed_restart("kind-control-plane", old_log_count)

    # Test restarting calico-node for the rb-server pod's node.
    def suppress_restart_calico_node_rb_server(self):
        old_log_count = len(get_early_logs("kind-worker3"))
        self._run_single_test(
            "restart_calico_node_rb_server",
            lambda: delete_calico_node_pod_for_node("kind-worker3"),
            lambda: restart_calico_node_pod_for_node("kind-worker3"),
        )
        self.check_early_container_noticed_restart("kind-worker3", old_log_count)

    def check_early_container_noticed_restart(self, node_name, old_log_count):
        new_logs = get_early_logs(node_name)[old_log_count:]
        found_stopped = False
        for new_log in new_logs:
            if "Normal BGP stopped; wait for graceful restart period" in new_log:
                found_stopped = True
                break
        assert found_stopped


# FailoverCluster holds methods to setup/cleanup testing enviroment.
class FailoverCluster(object):
    #
    #             +--------------------+-----plane 1----------+------------------+
    #             |                    |                      |                  |
    #             |   +--------------------+-------plane 2--------+------------------+
    #             |   |                |   |                  |   |              |   |
    #   +--------------------+  +-----------------+     +----------------+  +----------------+
    #   | kind-control-plane |  | kind-worker     |     | kind-worker2   |  | kind-worker3   |
    #   |                    |  |                 |     |                |  |                |
    #   |  POD: ra-server    |  | POD: client     |     | Target for     |  | POD: rb-server |
    #   |                    |  | HN: client-host |     | NodePort tests |  |                |
    #   +--------------------+  +-----------------+     +----------------+  +----------------+
    #
    #
    # PodIP tests: client -> ra-server pod IP
    #              client -> rb-server pod IP
    #
    # ServiceIP tests: client -> ra-server service cluster IP
    #                  client -> rb-server service cluster IP
    #
    # NodePort tests: client -> ra-server service node port on kind-worker2
    #                 client -> rb-server service node port on kind-worker2
    #
    # HostAccess tests: client-host -> ra-server pod IP
    #                   client-host -> rb-server pod IP
    #
    def setup(self):
        kubectl("create ns dualtor")

        # Create client, ra-server, rb-server and service.
        kubectl("run --generator=run-pod/v1 client -n dualtor" +
                " --image calico-test/busybox-with-reliable-nc --image-pull-policy Never --labels='pod-name=client' " +
                " --overrides='{ \"apiVersion\": \"v1\", \"spec\": { \"nodeSelector\": { \"kubernetes.io/hostname\": \"kind-worker\" }, \"terminationGracePeriodSeconds\": 0 } }'" +
                " --command /bin/sleep -- 3600")
        kubectl("run --generator=run-pod/v1 client-host -n dualtor" +
                " --image calico-test/busybox-with-reliable-nc --image-pull-policy Never --labels='pod-name=client-host' " +
                " --overrides='{ \"apiVersion\": \"v1\", \"spec\": { \"hostNetwork\": true, \"nodeSelector\": { \"kubernetes.io/hostname\": \"kind-worker\" }, \"terminationGracePeriodSeconds\": 0 } }'" +
                " --command /bin/sleep -- 3600")
        kubectl("run --generator=run-pod/v1 ra-server -n dualtor" +
                " --image calico-test/busybox-with-reliable-nc --image-pull-policy Never --labels='pod-name=ra-server,app=server' " +
                " --overrides='{ \"apiVersion\": \"v1\", \"spec\": { \"nodeSelector\": { \"kubernetes.io/hostname\": \"kind-control-plane\" }, \"terminationGracePeriodSeconds\": 0 } }'" +
                " --command /bin/sleep -- 3600")
        kubectl("run --generator=run-pod/v1 rb-server -n dualtor" +
                " --image calico-test/busybox-with-reliable-nc --image-pull-policy Never --labels='pod-name=rb-server,app=server' " +
                " --overrides='{ \"apiVersion\": \"v1\", \"spec\": { \"nodeSelector\": { \"kubernetes.io/hostname\": \"kind-worker3\" }, \"terminationGracePeriodSeconds\": 0 } }'" +
                " --command /bin/sleep -- 3600")
        kubectl("wait --timeout=1m --for=condition=ready" +
                " pod/client -n dualtor")
        kubectl("wait --timeout=1m --for=condition=ready" +
                " pod/client-host -n dualtor")
        kubectl("wait --timeout=1m --for=condition=ready" +
                " pod/ra-server -n dualtor")
        kubectl("wait --timeout=1m --for=condition=ready" +
                " pod/rb-server -n dualtor")

        # Create service
        self.create_service("ra-server")
        self.create_service("rb-server")
        self.create_service("ra-server", "-short", 8091)
        self.create_service("rb-server", "-short", 8091)

        # Check we can now exec into all the pods.
        def check_exec():
            kubectl("exec client -n dualtor -- date")
            kubectl("exec client-host -n dualtor -- date")
            kubectl("exec ra-server -n dualtor -- date")
            kubectl("exec rb-server -n dualtor -- date")

        retry_until_success(check_exec, retries=5, wait_time=1)

    def cleanup(self):
        kubectl("delete ns dualtor")

    def create_service(self, name, svc_suffix="", port=8090):
        service = client.V1Service(
            metadata=client.V1ObjectMeta(
                name=name + svc_suffix,
                labels={"name": name + svc_suffix},
            ),
            spec={
                "ports": [{"port": port}],
                "selector": {"pod-name": name},
                "type": "NodePort",
            }
        )
        config.load_kube_config(os.environ.get('KUBECONFIG'))
        api_response = client.CoreV1Api().create_namespaced_service(
            body=service,
            namespace="dualtor",
        )


class TestFailoverPodIP(_FailoverTest):

    def setUp(self):
        super(TestFailoverPodIP, self).setUp()
        self.config = FailoverTestConfig(2000, 10, [
            Flow("client", "ra-server", get_pod_ip("pod-name", "ra-server"), "8090", get_pod_ip("pod-name", "ra-server"), "8091"),
            Flow("client", "rb-server", get_pod_ip("pod-name", "rb-server"), "8090", get_pod_ip("pod-name", "rb-server"), "8091"),
        ])


class TestFailoverServiceIP(_FailoverTest):

    def setUp(self):
        super(TestFailoverServiceIP, self).setUp()
        self.config = FailoverTestConfig(2000, 10, [
            Flow("client", "ra-server", get_service_ip("ra-server"), "8090", get_service_ip("ra-server-short"), "8091"),
            Flow("client", "rb-server", get_service_ip("rb-server"), "8090", get_service_ip("rb-server-short"), "8091"),
        ])


class _TestFailoverNodePort(_FailoverTest):

    def setUp(self):
        super(TestFailoverNodePort, self).setUp()
        # Find node loopback address for kind-worker2.
        cmd='''docker exec kind-worker2 sh -c "ip a show dev lo | grep global | awk '{print \$2;}' | cut -f1 -d/"'''
        node_port_ip=subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT).strip()
        self.config = FailoverTestConfig(2000, 10, [
            Flow("client", "ra-server", node_port_ip, get_node_port("ra-server"), node_port_ip, get_node_port("ra-server-short")),
            Flow("client", "rb-server", node_port_ip, get_node_port("rb-server"), node_port_ip, get_node_port("rb-server-short")),
        ])

    # Test restarting calico-node on the NodePort node.
    def suppress_restart_calico_node_node_port(self):
        old_log_count = len(get_early_logs("kind-worker2"))
        self._run_single_test(
            "restart_calico_node_node_port",
            lambda: delete_calico_node_pod_for_node("kind-worker2"),
            lambda: restart_calico_node_pod_for_node("kind-worker2"),
        )
        self.check_early_container_noticed_restart("kind-worker2", old_log_count)


class TestFailoverHostAccess(_FailoverTest):

    def setUp(self):
        super(TestFailoverHostAccess, self).setUp()
        self.config = FailoverTestConfig(2000, 10, [
            Flow("client-host", "ra-server", get_pod_ip("pod-name", "ra-server"), "8090", get_pod_ip("pod-name", "ra-server"), "8091"),
            Flow("client-host", "rb-server", get_pod_ip("pod-name", "rb-server"), "8090", get_pod_ip("pod-name", "rb-server"), "8091"),
        ])


def ensureCalicoReady():

    def assertCalicoReady():
        for node in ["kind-control-plane", "kind-worker", "kind-worker2", "kind-worker3"]:
            get_calico_node_pod_for_node(node)
        kubectl("wait po -l k8s-app=calico-node -n calico-system --timeout=2m --for=condition=ready")
        for node in ["kind-control-plane", "kind-worker", "kind-worker2", "kind-worker3"]:
            pod = get_calico_node_pod_for_node(node)
            out = kubectl("exec %s -n calico-system -- birdcl show protocols" % pod)
            bgp_established = 0
            for line in out.splitlines():
                if "BGP" in line and "Established" in line:
                    bgp_established += 1
            assert bgp_established == 2, "Only %d established BGP sessions on %s" % (bgp_established, node)

    retry_until_success(assertCalicoReady, retries=12, wait_time=10)


class TestRestartCalicoNodes(TestBase):

    def get_restart_node_pod_name(self):
        self.restart_pod_name = kubectl(
            "get po -n calico-system" +
            " -l k8s-app=calico-node" +
            " --field-selector status.podIP=" + self.restart_node_ip +
            " -o jsonpath='{.items[*].metadata.name}'")
        if self.restart_pod_name == "":
            raise Exception('pod name not found')

    def test_restart_calico_nodes(self):
        for node_ip in ["172.31.10.3",
                        "172.31.10.4",
                        "172.31.20.3",
                        "172.31.20.4"]:

            # Get the name of the calico/node pod with that IP.
            self.restart_node_ip = node_ip
            self.get_restart_node_pod_name()

            # Delete it.
            kubectl("delete po %s -n calico-system" % self.restart_pod_name)

            # Wait until a replacement calico-node pod has been created.
            retry_until_success(self.get_restart_node_pod_name, retries=10, wait_time=1)

            # Wait until it is ready, before returning.
            kubectl("wait po %s -n calico-system --timeout=2m --for=condition=ready" %
                self.restart_pod_name)

            # Wait another 2s before moving on.
            time.sleep(2)


TestFailoverPodIP.vanilla = False
TestFailoverPodIP.dual_stack = False
TestFailoverPodIP.dual_tor = True
TestFailoverServiceIP.vanilla = False
TestFailoverServiceIP.dual_stack = False
TestFailoverServiceIP.dual_tor = True
_TestFailoverNodePort.vanilla = False
_TestFailoverNodePort.dual_stack = False
_TestFailoverNodePort.dual_tor = True
TestFailoverHostAccess.vanilla = False
TestFailoverHostAccess.dual_stack = False
TestFailoverHostAccess.dual_tor = True
TestRestartCalicoNodes.vanilla = False
TestRestartCalicoNodes.dual_stack = False
TestRestartCalicoNodes.dual_tor = True

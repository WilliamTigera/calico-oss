# Copyright (c) 2018 Tigera, Inc. All rights reserved.
#
FROM haproxy:1.8.14-alpine
MAINTAINER Karthik Ramasubramanian <karthik@tigera.io>

COPY haproxy.cfg /usr/local/etc/haproxy/haproxy.cfg
COPY rsyslog.conf /etc/rsyslog.conf

RUN apk update && apk add rsyslog

# Hack to launch rsyslogd as well as haproxy in the same container.
CMD ["sh", "-c", "/usr/sbin/rsyslogd && /usr/local/sbin/haproxy -W -db -f /usr/local/etc/haproxy/haproxy.cfg"]

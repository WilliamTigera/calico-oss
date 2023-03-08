# Copyright (c) 2018-2021 Tigera, Inc. All rights reserved.
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

FROM ruby:2.7-alpine3.16 as builder

RUN apk update && apk upgrade \
&& apk add --no-cache \
        curl \
        jq \
        ruby ruby-irb ruby-etc ruby-webrick \
        tini \
 && apk add --no-cache --virtual .build-deps \
        build-base linux-headers \
        ruby-dev gnupg \
 && echo 'gem: --no-document' >> /etc/gemrc \
 && gem install oj -v 3.10.18 \
 && gem install json -v 2.6.2 \
 && gem install async-http -v 0.54.0 \
 && gem install ext_monitor -v 0.1.2 \
 && gem install fluentd -v 1.15.3 \
 && gem install bigdecimal -v 1.4.4 \
 && gem install resolv -v 0.2.1 \
 && gem install \
        bundler:2.4.7 \
        cgi:0.3.6 \
        elasticsearch-api:7.17.7 \
        elasticsearch-transport:7.17.7 \
        elasticsearch-xpack:7.17.7 \
        elasticsearch:7.17.7 \
        fluent-plugin-cloudwatch-logs:0.8.0 \
        fluent-plugin-elasticsearch:5.2.4 \
        fluent-plugin-prometheus:2.0.0 \
        fluent-plugin-s3:1.3.0 \
        fluent-plugin-splunk-hec:1.1.2 \
        fluent-plugin-sumologic_output:1.6.1 \
        # to reduce windows build effort, psych is pinned at v4.
        # bundled libyaml is removed in v5.0.0 (see https://github.com/ruby/psych/pull/541)
        psych:4.0.6 \
        rdoc:6.4.0 \
 && fluent-gem install fluent-plugin-remote_syslog:1.1.0 \
 && gem sources --clear-all

# Configure scripts and create directories fluentD needs

RUN mkdir -p /fluentd/etc /fluentd/log /fluentd/plugins

COPY readiness.sh /bin/
RUN chmod +x /bin/readiness.sh

COPY liveness.sh /bin/
RUN chmod +x /bin/liveness.sh

COPY syslog-environment.sh /bin/
COPY syslog-config.sh /bin/
RUN chmod +x /bin/syslog-config.sh /bin/syslog-environment.sh

COPY splunk-environment.sh /bin/
RUN chmod +x /bin/splunk-environment.sh

COPY splunk-config.sh /bin/
RUN chmod +x /bin/splunk-config.sh

COPY sumo-environment.sh /bin/
RUN chmod +x /bin/sumo-environment.sh

COPY sumo-config.sh /bin/
RUN chmod +x /bin/sumo-config.sh

COPY ee_entrypoint.sh /bin/
RUN chmod +x /bin/ee_entrypoint.sh

COPY eks/bin/eks-log-forwarder-startup /bin/

RUN mkdir /fluentd/etc/output_flows
RUN mkdir /fluentd/etc/output_dns
RUN mkdir /fluentd/etc/output_tsee_audit
RUN mkdir /fluentd/etc/output_kube_audit
RUN mkdir /fluentd/etc/output_compliance_reports
RUN mkdir /fluentd/etc/output_bgp
RUN mkdir /fluentd/etc/output_ids_events
RUN mkdir /fluentd/etc/output_l7
RUN mkdir /fluentd/etc/output_runtime
RUN mkdir /fluentd/etc/output_waf

# Cleanup /tmp for the next stage
RUN rm -fr /tmp/*

################################################################################
# Build the actual scratch-based fluentd image
################################################################################

FROM scratch

ENV PATH=$PATH:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

ENV LD_PRELOAD=""
ENV RUBYLIB="/usr/lib/ruby/gems/3.0.0/gems/resolv-0.2.1/lib"
ENV FLUENTD_CONF="fluent.conf"

# Copy fluentd and ruby
COPY --from=builder /fluentd/ /fluentd/
COPY --from=builder /var/lib/ruby/ /var/lib/ruby/

# Copy binaries needed by fluentd scripts
COPY --from=builder /bin/bash /bin/bash
COPY --from=builder /bin/sh /bin/sh
COPY --from=builder /usr/bin/bash /usr/bin/bash
COPY --from=builder /bin/sed /bin/sed
COPY --from=builder /bin/test /bin/test
COPY --from=builder /bin/echo /bin/echo
COPY --from=builder /bin/cat /bin/cat
COPY --from=builder /bin/cp /bin/cp
COPY --from=builder /bin/curl /bin/curl
COPY --from=builder /bin/awk /bin/awk
COPY --from=builder /bin/sort /bin/sort
COPY --from=builder /bin/ls /bin/ls
COPY --from=builder /bin/which /bin/which
COPY --from=builder /usr/bin/coreutils /usr/bin/coreutils
COPY --from=builder /usr/bin/rm /usr/bin/rm
COPY --from=builder /usr/bin/tar /usr/bin/tar
COPY --from=builder /usr/bin/jq /usr/bin/jq
COPY --from=builder /usr/bin/fluentd /usr/bin/fluentd
COPY --from=builder /usr/bin/fluent-gem /usr/bin/fluent-gem

# Copy scripts needed by fluentd
COPY --from=builder /bin/readiness.sh /bin/readiness.sh
COPY --from=builder /bin/liveness.sh /bin/liveness.sh
COPY --from=builder /bin/syslog-environment.sh /bin/syslog-environment.sh
COPY --from=builder /bin/syslog-config.sh /bin/syslog-config.sh
COPY --from=builder /bin/splunk-environment.sh /bin/splunk-environment.sh
COPY --from=builder /bin/splunk-config.sh /bin/splunk-config.sh
COPY --from=builder /bin/sumo-environment.sh /bin/sumo-environment.sh
COPY --from=builder /bin/sumo-config.sh /bin/sumo-config.sh
COPY --from=builder /bin/ee_entrypoint.sh /bin/ee_entrypoint.sh
COPY --from=builder /bin/eks-log-forwarder-startup /bin/

# We copy everything from /lib64 because Fluentd requires a large number of libraries to be copied over
# It can run in multiple flavours (splunk, syslog) etc and those configurations will be done at runtime
COPY --from=builder /lib64/ /lib64/

# Create /tmp directory
COPY --from=builder /tmp /tmp/

# libc/nss
COPY --from=builder /etc/group /etc/group
COPY --from=builder /etc/hosts /etc/hosts
COPY --from=builder /etc/networks /etc/networks
COPY --from=builder /etc/nsswitch.conf /etc/nsswitch.conf
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/shadow /etc/shadow

# Copy scripts and configuration files for fluentd
COPY elastic_mapping_flows.template /fluentd/etc/elastic_mapping_flows.template
COPY elastic_mapping_dns.template /fluentd/etc/elastic_mapping_dns.template
COPY elastic_mapping_audits.template /fluentd/etc/elastic_mapping_audits.template
COPY elastic_mapping_bgp.template /fluentd/etc/elastic_mapping_bgp.template
COPY elastic_mapping_waf.template /fluentd/etc/elastic_mapping_waf.template
COPY elastic_mapping_l7.template /fluentd/etc/elastic_mapping_l7.template
COPY elastic_mapping_runtime.template /fluentd/etc/elastic_mapping_runtime.template
COPY fluent_sources.conf /fluentd/etc/fluent_sources.conf
COPY fluent_transforms.conf /fluentd/etc/fluent_transforms.conf
COPY output_match /fluentd/etc/output_match
COPY outputs /fluentd/etc/outputs
COPY inputs /fluentd/etc/inputs
COPY filters /fluentd/etc/filters
COPY rubyplugin /fluentd/plugins

# Compliance reports logs needs a regex pattern because there will be
# multiple logs (one per report type), e.g. compliance.network-access.reports.log
ENV COMPLIANCE_LOG_FILE=/var/log/calico/compliance/compliance.*.reports.log
ENV FLOW_LOG_FILE=/var/log/calico/flowlogs/flows.log
ENV DNS_LOG_FILE=/var/log/calico/dnslogs/dns.log
ENV BIRD_LOG_FILE=/var/log/calico/bird/current
ENV BIRD6_LOG_FILE=/var/log/calico/bird6/current
ENV IDS_EVENT_LOG_FILE=/var/log/calico/ids/events.log
ENV WAF_LOG_FILE=/var/log/calico/waf/waf.log
ENV L7_LOG_FILE=/var/log/calico/l7logs/l7.log
ENV EE_AUDIT_LOG_FILE=/var/log/calico/audit/tsee-audit.log
ENV RUNTIME_LOG_FILE=/var/log/calico/runtime-security/report.log

# TLS Settings
ENV TLS_KEY_PATH=/tls/tls.key
ENV TLS_CRT_PATH=/tls/tls.crt
ENV CA_CRT_PATH=/etc/pki/tigera/tigera-ca-bundle.crt

ENV POS_DIR=/var/log/calico

ENV ELASTIC_HOST=elasticsearch
ENV ELASTIC_PORT=9200
ENV ELASTIC_FLUSH_INTERVAL=5s

ENV KUBE_AUDIT_LOG=/var/log/calico/audit/kube-audit.log
ENV KUBE_AUDIT_POS=${POS_DIR}/kube-audit.log.pos

ENV ELASTIC_INDEX_SUFFIX=cluster

ENV S3_FLUSH_INTERVAL=5s
ENV S3_STORAGE=false

ENV ELASTIC_FLOWS_INDEX_SHARDS=1
ENV ELASTIC_FLOWS_INDEX_REPLICAS=0
ENV ELASTIC_DNS_INDEX_SHARDS=1
ENV ELASTIC_DNS_INDEX_REPLICAS=0
ENV ELASTIC_AUDIT_INDEX_REPLICAS=0
ENV ELASTIC_L7_INDEX_SHARDS=1
ENV ELASTIC_L7_INDEX_REPLICAS=0
ENV ELASTIC_WAF_INDEX_SHARDS=1
ENV ELASTIC_WAF_INDEX_REPLICAS=0
ENV ELASTIC_TEMPLATE_OVERWRITE=true
ENV ELASTIC_BGP_INDEX_SHARDS=1
ENV ELASTIC_BGP_INDEX_REPLICAS=0
ENV ELASTIC_RUNTIME_INDEX_SHARDS=1
ENV ELASTIC_RUNTIME_INDEX_REPLICAS=0

ENV SYSLOG_PACKET_SIZE=1024

#LINSEED DEFAULT PARAMS
ENV LINSEED_ENABLED=false
ENV LINSEED_ENDPOINT=ENDPOINT
ENV LINSEED_TOKEN=SAMPLETOKEN
ENV LINSEED_CA_PATH=/etc/flu/ca.pem
ENV LINSEED_CERT_PATH=/etc/flu/crt.pem
ENV LINSEED_KEY_PATH=/etc/flu/key.pem
ENV LINSEED_FLUSH_INTERVAL=5s


EXPOSE 24284

ENTRYPOINT []
CMD /bin/ee_entrypoint.sh fluentd -c /fluentd/etc/${FLUENTD_CONF} -p /fluentd/plugins $FLUENTD_OPT

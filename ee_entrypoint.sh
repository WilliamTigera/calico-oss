#!/bin/sh
set -e

remove_secure_es_conf() {
  if test -f "/fluentd/etc/output_${1}/out-es.conf"; then
    sed -i 's|scheme .*||g' /fluentd/etc/output_${1}/out-es.conf
    sed -i 's|user .*||g' /fluentd/etc/output_${1}/out-es.conf
    sed -i 's|password .*||g' /fluentd/etc/output_${1}/out-es.conf
    sed -i 's|ca_file .*||g' /fluentd/etc/output_${1}/out-es.conf
    sed -i 's|ssl_verify .*||g' /fluentd/etc/output_${1}/out-es.conf
  fi
}

# fluentd tries to watch Docker logs and write everything to screen
# by default: prevent this.
echo > /fluentd/etc/fluent.conf

if [ "${MANAGED_K8S}" == "true" ]; then
  # Managed Kubernetes (only EKS supported for now) needs an additional fluentd instance to
  # scrape kube-apiserver audit logs from CloudWatch.
  # This runs as an additional fluentd instance in the cluster, so it doesn't need to include any
  # of the regular logging that follows this if block.  Those logs are scraped by a separate
  # fluentd instance.

  # source
  if [ "${K8S_PLATFORM}" == "eks" ]; then
    export EKS_CLOUDWATCH_LOG_STREAM_PREFIX=${EKS_CLOUDWATCH_LOG_STREAM_PREFIX:-"kube-apiserver-audit-"}
    cat /fluentd/etc/inputs/in-eks.conf >> /fluentd/etc/fluent.conf
  fi

  # filter
  cat /fluentd/etc/filters/filter-eks-audit.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf

  # match
  if [ -z ${DISABLE_ES_AUDIT_KUBE_LOG} ] || [ "${DISABLE_ES_AUDIT_KUBE_LOG}" == "false" ]; then
    cp /fluentd/etc/outputs/out-es-kube-audit.conf /fluentd/etc/output_kube_audit/out-es.conf
    if [ -z ${FLUENTD_ES_SECURE} ] || [ "${FLUENTD_ES_SECURE}" == "false" ]; then
        remove_secure_es_conf kube_audit
    fi
  fi
  if [ "${S3_STORAGE}" == "true" ]; then
    cp /fluentd/etc/outputs/out-s3-kube-audit.conf /fluentd/etc/output_kube_audit/out-s3.conf
  fi

  source /bin/syslog-environment.sh
  source /bin/syslog-config.sh

  source /bin/splunk-environment.sh
  source /bin/splunk-config.sh
  
  source /bin/sumo-environment.sh
  source /bin/sumo-config.sh
  
  cat /fluentd/etc/outputs/out-eks-audit-es.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf

  # Run fluentd
  "$@"

  # bail earlier
  exit $?
fi

# Set the number of shards and replicas for index tigera_secure_ee_flows
sed -i 's|"number_of_shards": *[0-9]\+|"number_of_shards": '"$ELASTIC_FLOWS_INDEX_SHARDS"'|g' /fluentd/etc/elastic_mapping_flows.template
sed -i 's|"number_of_replicas": *[0-9]\+|"number_of_replicas": '"$ELASTIC_FLOWS_INDEX_REPLICAS"'|g' /fluentd/etc/elastic_mapping_flows.template

# Set the number of shards and replicas for index tigera_secure_ee_dns
sed -i 's|"number_of_shards": *[0-9]\+|"number_of_shards": '"$ELASTIC_DNS_INDEX_SHARDS"'|g' /fluentd/etc/elastic_mapping_dns.template
sed -i 's|"number_of_replicas": *[0-9]\+|"number_of_replicas": '"$ELASTIC_DNS_INDEX_REPLICAS"'|g' /fluentd/etc/elastic_mapping_dns.template

# Set the number of replicas for index tigera_secure_ee_audit
sed -i 's|"number_of_replicas": *[0-9]\+|"number_of_replicas": '"$ELASTIC_AUDIT_INDEX_REPLICAS"'|g' /fluentd/etc/elastic_mapping_audits.template

# Set the number of shards and replicas for index tigera_secure_ee_bgp
sed -i 's|"number_of_shards": *[0-9]\+|"number_of_shards": '"$ELASTIC_BGP_INDEX_SHARDS"'|g' /fluentd/etc/elastic_mapping_bgp.template
sed -i 's|"number_of_replicas": *[0-9]\+|"number_of_replicas": '"$ELASTIC_BGP_INDEX_REPLICAS"'|g' /fluentd/etc/elastic_mapping_bgp.template

# Set the number of shards and replicas for index tigera_secure_ee_l7
sed -i 's|"number_of_shards": *[0-9]\+|"number_of_shards": '"$ELASTIC_L7_INDEX_SHARDS"'|g' /fluentd/etc/elastic_mapping_l7.template
sed -i 's|"number_of_replicas": *[0-9]\+|"number_of_replicas": '"$ELASTIC_L7_INDEX_REPLICAS"'|g' /fluentd/etc/elastic_mapping_l7.template

# Build the fluentd configuration file bit by bit, because order is important.
# Add the sources.
cat /fluentd/etc/fluent_sources.conf >> /fluentd/etc/fluent.conf
echo >> /fluentd/etc/fluent.conf

# Append additional filter blocks to the fluentd config if provided.
if [ "${FLUENTD_FLOW_FILTERS}" == "true" ]; then
  cat /etc/fluentd/flow-filters.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Append additional filter blocks to the fluentd config if provided.
if [ "${FLUENTD_DNS_FILTERS}" == "true" ]; then
  cat /etc/fluentd/dns-filters.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Append additional filter blocks to the fluentd config if provided.
if [ "${FLUENTD_L7_FILTERS}" == "true" ]; then
  cat /etc/fluentd/l7-filters.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Record transformations to add additional identifiers.
cat /fluentd/etc/fluent_transforms.conf >> /fluentd/etc/fluent.conf
echo >> /fluentd/etc/fluent.conf

# Exclude specific ES outputs based on ENV variable flags. Note, if ES output is disabled here for a log type, depending on whether 
# another output destination is enabled, we may need to disable the output match directive for the log type completely (see later 
# on in this script).
if [ -z ${DISABLE_ES_FLOW_LOG} ] || [ "${DISABLE_ES_FLOW_LOG}" == "false" ]; then
  cp /fluentd/etc/outputs/out-es-flows.conf /fluentd/etc/output_flows/out-es.conf
fi
if [ -z ${DISABLE_ES_DNS_LOG} ] || [ "${DISABLE_ES_DNS_LOG}" == "false" ]; then
  cp /fluentd/etc/outputs/out-es-dns.conf /fluentd/etc/output_dns/out-es.conf
fi
if [ -z ${DISABLE_ES_AUDIT_EE_LOG} ] || [ "${DISABLE_ES_AUDIT_EE_LOG}" == "false" ]; then
  cp /fluentd/etc/outputs/out-es-tsee-audit.conf /fluentd/etc/output_tsee_audit/out-es.conf
fi
if [ -z ${DISABLE_ES_AUDIT_KUBE_LOG} ] || [ "${DISABLE_ES_AUDIT_KUBE_LOG}" == "false" ]; then
  cp /fluentd/etc/outputs/out-es-kube-audit.conf /fluentd/etc/output_kube_audit/out-es.conf
fi
if [ -z ${DISABLE_ES_BGP_LOG} ] || [ "${DISABLE_ES_BGP_LOG}" == "false" ]; then
  cp /fluentd/etc/outputs/out-es-bgp.conf /fluentd/etc/output_bgp/out-es.conf
fi
if [ -z ${DISABLE_ES_L7_LOG} ] || [ "${DISABLE_ES_L7_LOG}" == "false" ]; then
  cp /fluentd/etc/outputs/out-es-l7.conf /fluentd/etc/output_l7/out-es.conf
fi
# Check if we should strip out the secure settings from the configuration file.
if [ -z ${FLUENTD_ES_SECURE} ] || [ "${FLUENTD_ES_SECURE}" == "false" ]; then
  for x in flows dns tsee_audit kube_audit bgp l7; do
    remove_secure_es_conf $x
  done
fi

if [ "${S3_STORAGE}" == "true" ]; then
  cp /fluentd/etc/outputs/out-s3-flows.conf /fluentd/etc/output_flows/out-s3.conf
  cp /fluentd/etc/outputs/out-s3-dns.conf /fluentd/etc/output_dns/out-s3.conf
  cp /fluentd/etc/outputs/out-s3-tsee-audit.conf /fluentd/etc/output_tsee_audit/out-s3.conf
  cp /fluentd/etc/outputs/out-s3-kube-audit.conf /fluentd/etc/output_kube_audit/out-s3.conf
  cp /fluentd/etc/outputs/out-s3-compliance-reports.conf /fluentd/etc/output_compliance_reports/out-s3.conf
  cp /fluentd/etc/outputs/out-s3-l7.conf /fluentd/etc/output_l7/out-s3.conf
fi

source /bin/syslog-environment.sh
source /bin/syslog-config.sh

source /bin/splunk-environment.sh
source /bin/splunk-config.sh

source /bin/sumo-environment.sh
source /bin/sumo-config.sh

# Determine which output match directives to include.

# Include output destination for flow logs when (1) forwarding to ES is not disabled or (2) one of the other destinations for flows is turned on.
if [ -z ${DISABLE_ES_FLOW_LOG} ] || [ "${DISABLE_ES_FLOW_LOG}" == "false" ] || [ "${SYSLOG_FLOW_LOG}" == "true" ] || [ "${SPLUNK_FLOW_LOG}" == "true" ] || [ "${SUMO_FLOW_LOG}" == "true" ] || [ "${S3_STORAGE}" == "true" ]; then
  cat /fluentd/etc/output_match/flows.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Include output destination for DNS logs when (1) forwarding to ES is not disabled or (2) one of the other destinations for DNS is turned on.
if [ -z ${DISABLE_ES_DNS_LOG} ] || [ "${DISABLE_ES_DNS_LOG}" == "false" ] || [ "${SYSLOG_DNS_LOG}" == "true" ] || [ "${SPLUNK_DNS_LOG}" == "true" ] || [ "${SUMO_DNS_LOG}" == "true" ] || [ "${S3_STORAGE}" == "true" ]; then
  cat /fluentd/etc/output_match/dns.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Include output destination for L7 logs when (1) forwarding to ES is not disabled or (2) one of the other destinations for DNS is turned on.
if [ -z ${DISABLE_ES_L7_LOG} ] || [ "${DISABLE_ES_L7_LOG}" == "false" ] || [ "${SYSLOG_L7_LOG}" == "true" ] || [ "${SPLUNK_L7_LOG}" == "true" ] || [ "${SUMO_L7_LOG}" == "true" ] || [ "${S3_STORAGE}" == "true" ]; then
  cat /fluentd/etc/output_match/l7.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Include output destination for EE Audit logs when (1) forwarding to ES is not disabled or (2) one of the other destinations for EE Audit is turned on.
if [ -z ${DISABLE_ES_AUDIT_EE_LOG} ] || [ "${DISABLE_ES_AUDIT_EE_LOG}" == "false" ] || [ "${SYSLOG_AUDIT_EE_LOG}" == "true" ] || [ "${SPLUNK_AUDIT_TSEE_LOG}" == "true" ] || [ "${SUMO_AUDIT_TSEE_LOG}" == "true" ] || [ "${S3_STORAGE}" == "true" ]; then
  cat /fluentd/etc/output_match/audit-ee.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Include output destination for Kube Audit logs when (1) forwarding to ES is not disabled or (2) one of the other destinations for Kube Audit is turned on.
if [ -z ${DISABLE_ES_AUDIT_KUBE_LOG} ] || [ "${DISABLE_ES_AUDIT_KUBE_LOG}" == "false" ] || [ "${SYSLOG_AUDIT_KUBE_LOG}" == "true" ] || [ "${SPLUNK_AUDIT_KUBE_LOG}" == "true" ] || [ "${SUMO_AUDIT_KUBE_LOG}" == "true" ] || [ "${S3_STORAGE}" == "true" ]; then
  cat /fluentd/etc/output_match/audit-kube.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Include output destination for BGP logs when forwarding to ES is not disabled. Currently, BGP logs do not get forwarded to any other 
# destinations other than ES (may change in the future).
if [ -z ${DISABLE_ES_BGP_LOG} ] || [ "${DISABLE_ES_BGP_LOG}" == "false" ]; then
  cat /fluentd/etc/output_match/bgp.conf >> /fluentd/etc/fluent.conf
  echo >> /fluentd/etc/fluent.conf
fi

# Append additional output config (for Compliance reports) when S3 archiving is turned on.
if [ "${S3_STORAGE}" == "true" ]; then
  cat /fluentd/etc/output_match/compliance.conf >> /fluentd/etc/fluent.conf
fi
echo >> /fluentd/etc/fluent.conf

# Run fluentd
"$@"


##### SumoLogic #####
if [[ "${SUMO_AUDIT_LOG}" == "true" || "${SUMO_AUDIT_TSEE_LOG}" == "true" || "${SUMO_AUDIT_KUBE_LOG}" == "true" || "${SUMO_FLOW_LOG}" == "true" ]]; then
  # Optional SumoLogic audit log output
  if [ -z "${SUMO_AUDIT_SOURCE_CATEGORY}" ]; then
    sed -i 's|source_category .*||g' /fluentd/etc/outputs/out-sumologic-audit.conf
  fi
  if [ -z "${SUMO_AUDIT_SOURCE_NAME}" ]; then
    sed -i 's|source_name .*||g' /fluentd/etc/outputs/out-sumologic-audit.conf
  fi
  if [ -z "${SUMO_AUDIT_SOURCE_HOST}" ]; then
    sed -i 's|source_host .*||g' /fluentd/etc/outputs/out-sumologic-audit.conf
  fi
  if [ "${SUMO_AUDIT_LOG}" == "true" ]; then
    cp /fluentd/etc/outputs/out-sumologic-audit.conf /fluentd/etc/output_tsee_audit/out-sumologic-audit.conf
    cp /fluentd/etc/outputs/out-sumologic-audit.conf /fluentd/etc/output_kube_audit/out-sumologic-audit.conf
  elif [ "${SUMO_AUDIT_TSEE_LOG}" == "true" ]; then
    cp /fluentd/etc/outputs/out-sumologic-audit.conf /fluentd/etc/output_tsee_audit/out-sumologic-audit.conf
  elif [ "${SUMO_AUDIT_KUBE_LOG}" == "true" ]; then
    cp /fluentd/etc/outputs/out-sumologic-audit.conf /fluentd/etc/output_kube_audit/out-sumologic-audit.conf
  fi

  # Optional SumoLogic flow log output
  if [ -z "${SUMO_FLOW_SOURCE_CATEGORY}" ]; then
    sed -i 's|source_category .*||g' /fluentd/etc/outputs/out-sumologic-flow.conf
  fi
  if [ -z "${SUMO_FLOW_SOURCE_NAME}" ]; then
    sed -i 's|source_name .*||g' /fluentd/etc/outputs/out-sumologic-flow.conf
  fi
  if [ -z "${SUMO_FLOW_SOURCE_HOST}" ]; then
    sed -i 's|source_host .*||g' /fluentd/etc/outputs/out-sumologic-flow.conf
  fi
  if [ "${SUMO_FLOW_LOG}" == "true" ]; then
    cp /fluentd/etc/outputs/out-sumologic-flow.conf /fluentd/etc/output_flows/out-sumologic-flow.conf
  fi

  # Optional SumoLogic dns log output
  if [ -z "${SUMO_DNS_SOURCE_CATEGORY}" ]; then
    sed -i 's|source_category .*||g' /fluentd/etc/outputs/out-sumologic-dns.conf
  fi
  if [ -z "${SUMO_DNS_SOURCE_NAME}" ]; then
    sed -i 's|source_name .*||g' /fluentd/etc/outputs/out-sumologic-dns.conf
  fi
  if [ -z "${SUMO_DNS_SOURCE_HOST}" ]; then
    sed -i 's|source_host .*||g' /fluentd/etc/outputs/out-sumologic-dns.conf
  fi
  if [ "${SUMO_DNS_LOG}" == "true" ]; then
    cp /fluentd/etc/outputs/out-sumologic-dns.conf /fluentd/etc/output_dnss/out-sumologic-dns.conf
  fi

  # Optional SumoLogic l7 log output
  if [ -z "${SUMO_L7_SOURCE_CATEGORY}" ]; then
    sed -i 's|source_category .*||g' /fluentd/etc/outputs/out-sumologic-l7.conf
  fi
  if [ -z "${SUMO_L7_SOURCE_NAME}" ]; then
    sed -i 's|source_name .*||g' /fluentd/etc/outputs/out-sumologic-l7.conf
  fi
  if [ -z "${SUMO_L7_SOURCE_HOST}" ]; then
    sed -i 's|source_host .*||g' /fluentd/etc/outputs/out-sumologic-l7.conf
  fi
  if [ "${SUMO_L7_LOG}" == "true" ]; then
    cp /fluentd/etc/outputs/out-sumologic-l7.conf /fluentd/etc/output_l7/out-sumologic-l7.conf
  fi
fi


if [ -z "${SYSLOG_VERIFY_MODE}" ]; then
  sed -i 's|verify_mode .*||g' ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf
fi
if [ ! -f ${ROOT_DIR}/etc/fluentd/syslog/ca.pem ]; then
  sed -i 's|ca_file .*||g' ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf
fi
if [ "${SYSLOG_FLOW_LOG}" == "true" ]; then
  cp ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf ${ROOT_DIR}/fluentd/etc/output_flows/out-syslog.conf
fi
if [ "${SYSLOG_DNS_LOG}" == "true" ]; then
  cp ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf ${ROOT_DIR}/fluentd/etc/output_dns/out-syslog.conf
fi
if [ "${SYSLOG_AUDIT_EE_LOG}" == "true" ]; then
  cp ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf ${ROOT_DIR}/fluentd/etc/output_tsee_audit/out-syslog.conf
fi
if [ "${SYSLOG_AUDIT_KUBE_LOG}" == "true" ]; then
  cp ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf ${ROOT_DIR}/fluentd/etc/output_kube_audit/out-syslog.conf
fi
if [ "${SYSLOG_IDS_EVENT_LOG}" == "true" ]; then
  cp ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf ${ROOT_DIR}/fluentd/etc/output_ids_events/out-syslog.conf
fi
if [ "${SYSLOG_L7_LOG}" == "true" ]; then
  cp ${ROOT_DIR}/fluentd/etc/outputs/out-syslog.conf ${ROOT_DIR}/fluentd/etc/output_l7/out-syslog.conf
fi

# Append additional output matcher config (for IDS events) when SYSLOG forwarding is turned on
if [ "${SYSLOG_IDS_EVENT_LOG}" == "true" ]; then
  cat ${ROOT_DIR}/fluentd/etc/output_match/ids-events.conf >> ${ROOT_DIR}/fluentd/etc/fluent.conf
fi

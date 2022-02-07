#!/bin/bash

# This script will generate fluentd configs using the image
# tigera/fluentd:${IMAGETAG} based off the environment variables configurations
# below and then compare to previously captured configurations to ensure
# only expected changes have happened.

FAILED=0
TEST_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
mkdir -p $TEST_DIR/tmp

ADDITIONAL_MOUNT=""

function generateAndCollectConfig() {
  ENV_FILE=$1
  OUT_FILE=$2

  docker run -d --name generate-fluentd-config $ADDITIONAL_MOUNT --hostname config.generator --env-file $ENV_FILE tigera/fluentd:${IMAGETAG} >/dev/null
  sleep 2

  docker logs generate-fluentd-config | sed -n '/<ROOT>/,/<\/ROOT>/p' | sed -e 's|^.*<ROOT>|<ROOT>|' | sed -e 's/ \+$//' > $OUT_FILE
  if [ $? -ne 0 ]; then echo "Grabbing config from fluentd container failed"; exit 1; fi

  docker stop generate-fluentd-config >/dev/null
  if [ $? -ne 0 ]; then echo "Stopping fluentd container failed"; exit 1; fi

  docker rm generate-fluentd-config >/dev/null
  if [ $? -ne 0 ]; then echo "Removing fluentd container failed"; exit 1; fi

  unset ADDITIONAL_MOUNT
}

function checkConfiguration() {
  ENV_FILE=$1
  CFG_NAME=$2
  READABLE_NAME=$3

  EXPECTED=$TEST_DIR/$CFG_NAME.cfg
  UUT=$TEST_DIR/tmp/$CFG_NAME.cfg

  echo "#### Testing configuration of $READABLE_NAME"

  generateAndCollectConfig $ENV_FILE $UUT

  diff $EXPECTED $UUT &> /dev/null
  if [ $? -eq 0 ]; then
    echo "  ## configuration is correct"
  else
    echo " XXX configuration is not correct"
    FAILED=1
    diff $EXPECTED $UUT
  fi
}


STANDARD_ENV_VARS=$(cat << EOM
NODENAME=test-node-name
ELASTIC_INDEX_SUFFIX=test-cluster-name
ELASTIC_FLOWS_INDEX_SHARDS=5
ELASTIC_DNS_INDEX_SHARDS=5
ELASTIC_L7_INDEX_SHARDS=5
ELASTIC_RUNTIME_INDEX_SHARDS=5
ELASTIC_WAF_INDEX_SHARDS=5
FLUENTD_FLOW_FILTERS=# not a real filter
FLOW_LOG_FILE=/var/log/calico/flowlogs/flows.log
DNS_LOG_FILE=/var/log/calico/dnslogs/dns.log
L7_LOG_FILE=/var/log/calico/l7logs/l7.log
RUNTIME_LOG_FILE=/var/log/calico/runtime-security/report.log
WAF_LOG_FILE=/var/log/calico/waf/waf.log
ELASTIC_HOST=elasticsearch-tigera-elasticsearch.calico-monitoring.svc.cluster.local
ELASTIC_PORT=9200
EOM
)

ES_SECURE_VARS=$(cat <<EOM
ELASTIC_SSL_VERIFY=true
ELASTIC_USER=es-user
ELASTIC_PASSWORD=es-password
EOM
)

S3_VARS=$(cat <<EOM
AWS_KEY_ID=aws-key-id-value
AWS_SECRET_KEY=aws-secret-key-value
S3_STORAGE=true
S3_BUCKET_NAME=dummy-bucket
AWS_REGION=not-real-region
S3_BUCKET_PATH=not-a-bucket
S3_FLUSH_INTERVAL=30
EOM
)

SYSLOG_NO_TLS_VARS=$(cat <<EOM
SYSLOG_FLOW_LOG=true
SYSLOG_HOST=169.254.254.254
SYSLOG_PORT=3665
SYSLOG_PROTOCOL=udp
SYSLOG_HOSTNAME=nodename
SYSLOG_FLUSH_INTERVAL=17s
EOM
)

SYSLOG_TLS_VARS=$(cat <<EOM
SYSLOG_FLOW_LOG=true
SYSLOG_AUDIT_KUBE_LOG=true
SYSLOG_IDS_EVENT_LOG=true
SYSLOG_HOST=169.254.254.254
SYSLOG_PORT=3665
SYSLOG_PROTOCOL=tcp
SYSLOG_TLS=true
SYSLOG_VERIFY_MODE=\${OPENSSL::SSL::VERIFY_NONE}
SYSLOG_HOSTNAME=nodename
EOM
)

SYSLOG_TLS_VARS_ALL_LOG_TYPES=$(cat <<EOM
SYSLOG_FLOW_LOG=true
SYSLOG_DNS_LOG=true
SYSLOG_L7_LOG=true
SYSLOG_RUNTIME_LOG=true
SYSLOG_WAF_LOG=true
SYSLOG_AUDIT_EE_LOG=true
SYSLOG_AUDIT_KUBE_LOG=true
SYSLOG_IDS_EVENT_LOG=true
SYSLOG_HOST=169.254.254.254
SYSLOG_PORT=3665
SYSLOG_PROTOCOL=tcp
SYSLOG_TLS=true
SYSLOG_VERIFY_MODE=\${OPENSSL::SSL::VERIFY_NONE}
SYSLOG_HOSTNAME=nodename
EOM
)

EKS_VARS=$(cat <<EOM
MANAGED_K8S=true
K8S_PLATFORM=eks
EKS_CLOUDWATCH_LOG_GROUP=/aws/eks/eks-audit-test/cluster/
EKS_CLOUDWATCH_LOG_FETCH_INTERVAL=10
EOM
)

# Test with ES not secure
cat > $TEST_DIR/tmp/es-no-secure.env <<EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=false
EOM

checkConfiguration $TEST_DIR/tmp/es-no-secure.env es-no-secure "ES unsecure"

# Test with ES secure
cat > $TEST_DIR/tmp/es-secure.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=true
$ES_SECURE_VARS
EOM

checkConfiguration $TEST_DIR/tmp/es-secure.env es-secure "ES secure"

# Test with disabled ES secure (all log types)
cat > $TEST_DIR/tmp/disable-es-secure.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=true
$ES_SECURE_VARS
DISABLE_ES_FLOW_LOG=true
DISABLE_ES_DNS_LOG=true
DISABLE_ES_L7_LOG=true
DISABLE_ES_RUNTIME_LOG=true
DISABLE_ES_WAF_LOG=true
DISABLE_ES_AUDIT_EE_LOG=true
DISABLE_ES_AUDIT_KUBE_LOG=true
DISABLE_ES_BGP_LOG=true
EOM

checkConfiguration $TEST_DIR/tmp/disable-es-secure.env disable-es-secure "Disable ES secure"

# Test with some disabled ES secure
cat > $TEST_DIR/tmp/disable-some-es-secure.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=true
$ES_SECURE_VARS
DISABLE_ES_AUDIT_EE_LOG=true
DISABLE_ES_AUDIT_KUBE_LOG=true
DISABLE_ES_BGP_LOG=true
EOM

checkConfiguration $TEST_DIR/tmp/disable-some-es-secure.env disable-some-es-secure "Disable some ES secure"

# Test with disabled ES unsecure (all log types)
cat > $TEST_DIR/tmp/disable-es-unsecure.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=false
$ES_SECURE_VARS
DISABLE_ES_FLOW_LOG=true
DISABLE_ES_DNS_LOG=true
DISABLE_ES_L7_LOG=true
DISABLE_ES_RUNTIME_LOG=true
DISABLE_ES_WAF_LOG=true
DISABLE_ES_AUDIT_EE_LOG=true
DISABLE_ES_AUDIT_KUBE_LOG=true
DISABLE_ES_BGP_LOG=true
EOM

checkConfiguration $TEST_DIR/tmp/disable-es-unsecure.env disable-es-unsecure "Disable ES unsecure"

# Test with some disabled ES unsecure
cat > $TEST_DIR/tmp/disable-some-es-unsecure.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=false
$ES_SECURE_VARS
DISABLE_ES_AUDIT_EE_LOG=true
DISABLE_ES_AUDIT_KUBE_LOG=true
DISABLE_ES_BGP_LOG=true
EOM

checkConfiguration $TEST_DIR/tmp/disable-some-es-unsecure.env disable-some-es-unsecure "Disable some ES unsecure"

# Test with S3 and ES secure
cat > $TEST_DIR/tmp/es-secure-with-s3.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=true
$ES_SECURE_VARS
$S3_VARS
EOM

checkConfiguration $TEST_DIR/tmp/es-secure-with-s3.env es-secure-with-s3 "ES secure with S3"

# Test with S3 and ES not secure
cat > $TEST_DIR/tmp/es-no-secure-with-s3.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=false
$S3_VARS
EOM

checkConfiguration $TEST_DIR/tmp/es-no-secure-with-s3.env es-no-secure-with-s3 "ES unsecure with S3"

# Test with ES not secure and syslog w/no tls
cat > $TEST_DIR/tmp/es-no-secure-with-syslog-no-tls.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=false
$SYSLOG_NO_TLS_VARS
EOM

checkConfiguration $TEST_DIR/tmp/es-no-secure-with-syslog-no-tls.env es-no-secure-with-syslog-no-tls "ES unsecure with syslog without TLS"

# Test with ES secure and syslog with tls
cat > $TEST_DIR/tmp/es-secure-with-syslog-with-tls.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=true
$ES_SECURE_VARS
$SYSLOG_TLS_VARS
EOM

TMP=$(tempfile)
ADDITIONAL_MOUNT="-v $TMP:/etc/fluentd/syslog/ca.pem"
checkConfiguration $TEST_DIR/tmp/es-secure-with-syslog-with-tls.env es-secure-with-syslog-with-tls "ES secure with syslog with TLS"

# Test with ES secure and syslog with tls with all log types
cat > $TEST_DIR/tmp/es-secure-with-syslog-with-tls-all-log-types.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=true
$ES_SECURE_VARS
$SYSLOG_TLS_VARS_ALL_LOG_TYPES
EOM

TMP=$(tempfile)
ADDITIONAL_MOUNT="-v $TMP:/etc/fluentd/syslog/ca.pem"
checkConfiguration $TEST_DIR/tmp/es-secure-with-syslog-with-tls-all-log-types.env es-secure-with-syslog-with-tls-all-log-types "ES secure with syslog with TLS with all log types"

# Test with ES secure and syslog with tls
cat > $TEST_DIR/tmp/es-secure-with-syslog-and-s3.env << EOM
$STANDARD_ENV_VARS
FLUENTD_ES_SECURE=true
$ES_SECURE_VARS
$SYSLOG_TLS_VARS
$S3_VARS
EOM

checkConfiguration $TEST_DIR/tmp/es-secure-with-syslog-and-s3.env es-secure-with-syslog-and-s3 "ES secure with syslog and S3"

# Test with EKS
cat > $TEST_DIR/tmp/eks.env <<EOM
$EKS_VARS
EOM
checkConfiguration $TEST_DIR/tmp/eks.env eks "EKS"

# Test with EKS, Log Stream Prefix overwritten
cat > $TEST_DIR/tmp/eks-log-stream-pfx.env <<EOM
$EKS_VARS
EKS_CLOUDWATCH_LOG_STREAM_PREFIX=kube-apiserver-audit-overwritten-
EOM
checkConfiguration $TEST_DIR/tmp/eks-log-stream-pfx.env eks-log-stream-pfx "EKS - Log Stream Prefix overwritten"

SPLUNK_COMMON_VARS=$(cat <<EOM
SPLUNK_HEC_TOKEN=splunk-token
SPLUNK_FLOW_LOG=true
SPLUNK_AUDIT_LOG=true
SPLUNK_HEC_HOST=splunk.eng.tigera.com
SPLUNK_HEC_PORT=8088
SPLUNK_PROTOCOL=https
SPLUNK_FLUSH_INTERVAL=5
NODENAME=test-node-name
EOM
)

# Test with Splunk, normal server with http https
cat > $TEST_DIR/tmp/splunk-trusted-http-https.env << EOM
$SPLUNK_COMMON_VARS
EOM

checkConfiguration $TEST_DIR/tmp/splunk-trusted-http-https.env splunk-trusted-http-https "Splunk - with http and https"

## Test with Splunk, self signed ca certificate
cat > $TEST_DIR/tmp/splunk-self-signed-ca.env << EOM
$SPLUNK_COMMON_VARS
SPLUNK_CA_FILE=/etc/ssl/splunk/ca.pem
EOM

checkConfiguration $TEST_DIR/tmp/splunk-self-signed-ca.env splunk-self-signed-ca "Splunk - self signed ca config"

rm -f $TMP

exit $FAILED

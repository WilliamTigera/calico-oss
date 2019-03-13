#!/bin/sh

OUTPUT_FLOW_S3=$(cat <<EOM
  <store>
    @type s3
    aws_key_id "#{ENV['AWS_KEY_ID']}"
    aws_sec_key "#{ENV['AWS_SECRET_KEY']}"
    s3_bucket "#{ENV['S3_BUCKET_NAME']}"
    s3_region "#{ENV['AWS_REGION']}"
    path "#{ENV['S3_BUCKET_PATH']}/flows"
    <buffer>
      flush_mode interval
      flush_interval "#{ENV['S3_FLUSH_INTERVAL']}"
    </buffer>
  </store>
EOM
)

OUTPUT_AUDIT_TSEE_S3=$(cat <<EOM
  <store>
    @type s3
    aws_key_id "#{ENV['AWS_KEY_ID']}"
    aws_sec_key "#{ENV['AWS_SECRET_KEY']}"
    s3_bucket "#{ENV['S3_BUCKET_NAME']}"
    s3_region "#{ENV['AWS_REGION']}"
    path "#{ENV['S3_BUCKET_PATH']}/audit_tsee"
    <buffer>
      flush_mode interval
      flush_interval "#{ENV['S3_FLUSH_INTERVAL']}"
    </buffer>
  </store>
EOM
)

OUTPUT_AUDIT_KUBE_S3=$(cat <<EOM
  <store>
    @type s3
    aws_key_id "#{ENV['AWS_KEY_ID']}"
    aws_sec_key "#{ENV['AWS_SECRET_KEY']}"
    s3_bucket "#{ENV['S3_BUCKET_NAME']}"
    s3_region "#{ENV['AWS_REGION']}"
    path "#{ENV['S3_BUCKET_PATH']}/audit_kube"
    <buffer>
      flush_mode interval
      flush_interval "#{ENV['S3_FLUSH_INTERVAL']}"
    </buffer>
  </store>
EOM
)

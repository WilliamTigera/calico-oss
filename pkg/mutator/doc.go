package mutator

// The mutator package extracts policy impact params from an ES query and using that data modifies the ES response
// to include preview action.

// ---- Request Format ----
//
// https://127.0.0.1:30003/tigera-elasticsearch/tigera_secure_ee_flows.cluster.*/_search  POST
/*

{
  "query": {
    "bool": {
      "must": [
        {
          "range": {
            "end_time": {
              "gte": "now-15m",
              "lte": "now-0m"
            }
          }
        },
        {
          "terms": {
            "source_type": [
              "net",
              "ns",
              "wep",
              "hep"
            ]
          }
        },
        {
          "terms": {
            "dest_type": [
              "net",
              "ns",
              "wep",
              "hep"
            ]
          }
        }
      ]
    }
  },
  "size": 0,
  "aggs": {
    "flog_buckets": {
      "composite": {
        "size": 1000,
        "sources": [
          {
            "source_type": {
              "terms": {
                "field": "source_type"
              }
            }
          },
          {
            "source_namespace": {
              "terms": {
                "field": "source_namespace"
              }
            }
          },
          {
            "source_name": {
              "terms": {
                "field": "source_name_aggr"
              }
            }
          },
          {
            "dest_type": {
              "terms": {
                "field": "dest_type"
              }
            }
          },
          {
            "dest_namespace": {
              "terms": {
                "field": "dest_namespace"
              }
            }
          },
          {
            "dest_name": {
              "terms": {
                "field": "dest_name_aggr"
              }
            }
          },
          {
            "reporter": {
              "terms": {
                "field": "reporter"
              }
            }
          },
          {
            "action": {
              "terms": {
                "field": "action"
              }
            }
          }
        ]
      },
      "aggs": {
        "policies": {
          "nested": {
            "path": "policies"
          },
          "aggs": {
            "by_tiered_policy": {
              "terms": {
                "field": "policies.all_policies"
              }
            }
          }
        },
        "source_labels": {
          "nested": {
            "path": "source_labels"
          },
          "aggs": {
            "by_kvpair": {
              "terms": {
                "field": "source_labels.labels"
              }
            }
          }
        },
        "dest_labels": {
          "nested": {
            "path": "dest_labels"
          },
          "aggs": {
            "by_kvpair": {
              "terms": {
                "field": "dest_labels.labels"
              }
            }
          }
        },
        "sum_num_flows_started": {
          "sum": {
            "field": "num_flows_started"
          }
        },
        "sum_num_flows_completed": {
          "sum": {
            "field": "num_flows_completed"
          }
        },
        "sum_packets_in": {
          "sum": {
            "field": "packets_in"
          }
        },
        "sum_bytes_in": {
          "sum": {
            "field": "bytes_in"
          }
        },
        "sum_packets_out": {
          "sum": {
            "field": "packets_out"
          }
        },
        "sum_bytes_out": {
          "sum": {
            "field": "bytes_out"
          }
        },
        "sum_http_requests_allowed_in": {
          "sum": {
            "field": "http_requests_allowed_in"
          }
        },
        "sum_http_requests_denied_in": {
          "sum": {
            "field": "http_requests_denied_in"
          }
        }
      }
    }
  },
  "resourceActions": [
    {
      "action": "update",
      "resource": {
        "apiVersion": "projectcalico.org/v3",
        "kind": "NetworkPolicy",
        "metadata": {
          "uid": "14aeb6ce-a1c7-11e9-aaac-42010a800012",
          "name": "ttt.aaa",
          "namespace": "default",
          "resourceVersion": "757650"
        },
        "spec": {
          "tier": "ttt",
          "order": 0,
          "selector": "app == \"fe\"||color == \"blue\"",
          "types": [
            "Ingress"
          ]
        }
      }
    }
  ]
}

*/

// ---- Response Format ----
//
/*

{
    "_shards": {
        "failed": 0,
        "skipped": 0,
        "successful": 40,
        "total": 40
    },
    "aggregations": {
        "flog_buckets": {
            "after_key": {
                "action": "allow",
                "dest_name": "aws",
                "dest_namespace": "-",
                "dest_type": "net",
                "reporter": "src",
                "source_name": "coredns-86c58d9df4-*",
                "source_namespace": "kube-system",
                "source_type": "wep"
            },
            "buckets": [
                {
                    "dest_labels": {
                        "by_kvpair": {
                            "buckets": [
                                {
                                    "key": "apiserver=true"
                                },
                                {
                                    "key": "k8s-app=cnx-apiserver"
                                },
                                {
                                    "key": "pod-template-hash=65954bfd46"
                                }
                            ]
                        }
                    },
                    "key": {
                        "action": "allow",
                        "dest_name": "cnx-apiserver-65954bfd46-*",
                        "dest_namespace": "kube-system",
                        "dest_port": "",
                        "dest_type": "wep",
                        "preview_action": "deny",
                        "proto": "",
                        "reporter": "dst",
                        "source_name": "pvt",
                        "source_namespace": "-",
                        "source_type": "net"
                    },
                    "policies": {
                        "by_tiered_policy": {
                            "buckets": [
                                {
                                    "key": "0|allow-cnx|kube-system/allow-cnx.cnx-apiserver-access|allow"
                                }
                            ]
                        }
                    },
                    "source_labels": {
                        "by_kvpair": {
                            "buckets": []
                        }
                    }
                }
            ]
        }
    }
}

*/

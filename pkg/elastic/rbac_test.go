// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package elastic_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/tigera/es-proxy/pkg/elastic"

	"github.com/olivere/elastic/v7"

	"github.com/tigera/lma/pkg/httputils"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
)

var (
	authorizationMatrixAll = []apiv3.AuthorizedResourceVerbs{{
		APIGroup: "",
		Resource: "pods",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "",
			}},
		}},
	}, {
		APIGroup: "projectcalico.org",
		Resource: "networksets",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "",
			}},
		}},
	}, {
		APIGroup: "projectcalico.org",
		Resource: "globalnetworksets",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "",
			}},
		}},
	}, {
		APIGroup: "projectcalico.org",
		Resource: "hostendpoints",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "",
			}},
		}},
	}, {
		APIGroup: "projectcalico.org",
		Resource: "networkpolicies",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "",
			}},
		}},
	}}

	authorizationMatrixNamespaces = []apiv3.AuthorizedResourceVerbs{{
		APIGroup: "",
		Resource: "pods",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "ns1",
			}, {
				Namespace: "ns2",
			}},
		}},
	}, {
		APIGroup: "projectcalico.org",
		Resource: "networksets",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "ns2",
			}},
		}},
	}, {
		APIGroup: "projectcalico.org",
		Resource: "networkpolicies",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "ns3",
			}},
		}},
	}}

	authorizationMatrixSingle = []apiv3.AuthorizedResourceVerbs{{
		APIGroup: "",
		Resource: "pods",
		Verbs: []apiv3.AuthorizedResourceVerb{{
			Verb: "list",
			ResourceGroups: []apiv3.AuthorizedResourceGroup{{
				Namespace: "ns1",
			}},
		}},
	}}

	authorizationMatrixNone = []apiv3.AuthorizedResourceVerbs{}
)

func expectQueryJson(q elastic.Query, js string) {
	src, err := q.Source()
	Expect(err).NotTo(HaveOccurred())
	actual, err := json.Marshal(src)
	Expect(err).NotTo(HaveOccurred())
	Expect(actual).To(MatchJSON(js))
}

var _ = Describe("RBAC query tests", func() {
	It("handles flows with full perms", func() {
		query, err := GetRBACQueryForFlowLogs(authorizationMatrixAll)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "bool": {
          "should": [
            {
              "term": {
                "source_type": "wep"
              }
            },
            {
              "term": {
                "dest_type": "wep"
              }
            },
            {
              "bool": {
                "must": {
                  "term": {
                    "source_type": "ns"
                  }
                },
                "must_not": {
                  "term": {
                    "source_namespace": "-"
                  }
                }
              }
            },
            {
              "bool": {
                "must": {
                  "term": {
                    "dest_type": "ns"
                  }
                },
                "must_not": {
                  "term": {
                    "dest_namespace": "-"
                  }
                }
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "source_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "source_namespace": "-"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "-"
                    }
                  }
                ]
              }
            },
            {
              "term": {
                "source_type": "hep"
              }
            },
            {
              "term": {
                "dest_type": "hep"
              }
            }
          ]
        }
      }`)
	})

	It("handles flows with namespaced perms", func() {
		query, err := GetRBACQueryForFlowLogs(authorizationMatrixNamespaces)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "bool": {
          "should": [
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "source_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "source_namespace": "ns1"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns1"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "source_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "source_namespace": "ns2"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns2"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "source_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "source_namespace": "ns2"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns2"
                    }
                  }
                ]
              }
            }
          ]
        }
      }`)
	})

	It("handles flows with single perm", func() {
		query, err := GetRBACQueryForFlowLogs(authorizationMatrixSingle)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "bool": {
          "should": [
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "source_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "source_namespace": "ns1"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns1"
                    }
                  }
                ]
              }
            }
          ]
        }
      }`)
	})

	It("handles flows with no perms", func() {
		_, err := GetRBACQueryForFlowLogs(authorizationMatrixNone)
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(&httputils.HttpStatusError{}))
		Expect(err.(*httputils.HttpStatusError).Status).To(Equal(http.StatusForbidden))
	})

	It("handles l7 with full perms", func() {
		query, err := GetRBACQueryForL7Logs(authorizationMatrixAll)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "bool": {
          "should": [
            {
              "term": {
                "src_type": "wep"
              }
            },
            {
              "term": {
                "dest_type": "wep"
              }
            },
            {
              "bool": {
                "must": {
                  "term": {
                    "src_type": "ns"
                  }
                },
                "must_not": {
                  "term": {
                    "src_namespace": "-"
                  }
                }
              }
            },
            {
              "bool": {
                "must": {
                  "term": {
                    "dest_type": "ns"
                  }
                },
                "must_not": {
                  "term": {
                    "dest_namespace": "-"
                  }
                }
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "src_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "src_namespace": "-"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "-"
                    }
                  }
                ]
              }
            },
            {
              "term": {
                "src_type": "hep"
              }
            },
            {
              "term": {
                "dest_type": "hep"
              }
            }
          ]
        }
      }`)
	})

	It("handles l7 with namespaced perms", func() {
		query, err := GetRBACQueryForL7Logs(authorizationMatrixNamespaces)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "bool": {
          "should": [
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "src_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "src_namespace": "ns1"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns1"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "src_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "src_namespace": "ns2"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns2"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "src_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "src_namespace": "ns2"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "ns"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns2"
                    }
                  }
                ]
              }
            }
          ]
        }
      }`)
	})

	It("handles l7 with single perm", func() {
		query, err := GetRBACQueryForL7Logs(authorizationMatrixSingle)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "bool": {
          "should": [
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "src_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "src_namespace": "ns1"
                    }
                  }
                ]
              }
            },
            {
              "bool": {
                "must": [
                  {
                    "term": {
                      "dest_type": "wep"
                    }
                  },
                  {
                    "term": {
                      "dest_namespace": "ns1"
                    }
                  }
                ]
              }
            }
          ]
        }
      }`)
	})

	It("handles l7 with no perms", func() {
		_, err := GetRBACQueryForL7Logs(authorizationMatrixNone)
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(&httputils.HttpStatusError{}))
		Expect(err.(*httputils.HttpStatusError).Status).To(Equal(http.StatusForbidden))
	})

	It("handles dns with full perms", func() {
		query, err := GetRBACQueryForDNSLogs(authorizationMatrixAll)
		Expect(err).NotTo(HaveOccurred())
		Expect(query).To(BeNil())
	})

	It("handles dns with namespaced perms", func() {
		query, err := GetRBACQueryForDNSLogs(authorizationMatrixNamespaces)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "bool": {
          "should": [
            {
              "term": {
                "client_namespace": "ns1"
              }
            },
            {
              "term": {
                "client_namespace": "ns2"
              }
            }
          ]
        }
      }`)
	})

	It("handles dns with single perm", func() {
		query, err := GetRBACQueryForDNSLogs(authorizationMatrixSingle)
		Expect(err).NotTo(HaveOccurred())
		expectQueryJson(query, `{
        "term": {
          "client_namespace": "ns1"
        }
      }`)
	})

	It("handles dns with no perms", func() {
		_, err := GetRBACQueryForDNSLogs(authorizationMatrixNone)
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(&httputils.HttpStatusError{}))
		Expect(err.(*httputils.HttpStatusError).Status).To(Equal(http.StatusForbidden))
	})
})

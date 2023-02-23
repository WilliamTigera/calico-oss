// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package v1

import (
	"fmt"
	"net"
	"reflect"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/stretchr/testify/require"

	"github.com/google/gopacket/layers"
)

var (
	decodedDNSSOA = "tigera.io. root.tigera.io. 1 3600 60 86400 1800"
	decodedDNSSRV = "10 20 53 ns.tigera.io."
	decodedDNSMX  = "10 mail.tigera.io."
	dnsSRV        = layers.DNSSRV{
		Priority: 10,
		Weight:   20,
		Port:     53,
		Name:     []byte("ns.tigera.io."),
	}
	dnsSOA = layers.DNSSOA{
		MName:   []byte("tigera.io."),
		RName:   []byte("root.tigera.io."),
		Serial:  1,
		Refresh: 3600,
		Retry:   60,
		Expire:  86400,
		Minimum: 1800,
	}
	dnsMX = layers.DNSMX{
		Preference: 10,
		Name:       []byte("mail.tigera.io."),
	}
)

func TestDNSClass_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		d       DNSClass
		want    []byte
		wantErr bool
	}{
		{"DNSClassIN", DNSClass(layers.DNSClassIN), []byte("\"IN\""), false},
		{"DNSClassCS", DNSClass(layers.DNSClassCS), []byte("\"CS\""), false},
		{"DNSClassCH", DNSClass(layers.DNSClassCH), []byte("\"CH\""), false},
		{"DNSClassHS", DNSClass(layers.DNSClassHS), []byte("\"HS\""), false},
		{"DNSClassAny", DNSClass(layers.DNSClassAny), []byte("\"Any\""), false},
		{"Unmapped value", DNSClass(6), []byte("\"#6\""), false},
		{"Min value", DNSClass(0), []byte("\"#0\""), false},
		{"Max value", DNSClass(65535), []byte("\"#65535\""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.d.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func TestDNSClass_String(t *testing.T) {
	tests := []struct {
		name string
		d    DNSClass
		want string
	}{
		{"IN", DNSClass(layers.DNSClassIN), "IN"},
		{"CS", DNSClass(layers.DNSClassCS), "CS"},
		{"CH", DNSClass(layers.DNSClassCH), "CH"},
		{"HS", DNSClass(layers.DNSClassHS), "HS"},
		{"Any", DNSClass(layers.DNSClassAny), "Any"},
		{"Unmapped value", DNSClass(6), "#6"},
		{"Min value", DNSClass(0), "#0"},
		{"Max value", DNSClass(65535), "#65535"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDNSClass_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    DNSClass
		wantErr bool
	}{
		{"IN", []byte("\"IN\""), DNSClass(layers.DNSClassIN), false},
		{"CS", []byte("\"CS\""), DNSClass(layers.DNSClassCS), false},
		{"CH", []byte("\"CH\""), DNSClass(layers.DNSClassCH), false},
		{"HS", []byte("\"HS\""), DNSClass(layers.DNSClassHS), false},
		{"Any", []byte("\"Any\""), DNSClass(layers.DNSClassAny), false},
		{"Unmapped DNS code", []byte("\"#6\""), DNSClass(6), false},
		{"Any string", []byte("\"AnyString\""), DNSClass(0), false},
		{"Any integer", []byte("0"), DNSClass(0), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dnsClass := DNSClass(0)
			err := dnsClass.UnmarshalJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				require.NoError(t, err)
				if !reflect.DeepEqual(dnsClass, tt.want) {
					t.Errorf("UnmarshalJSON() got = %d, want %d", dnsClass, tt.want)
				}
			}
		})
	}
}

func TestDNSClass_NilPointerReceiver(t *testing.T) {
	t.Run("Nil Pointer Receiver - String()", func(t *testing.T) {
		var c *DNSClass
		require.Empty(t, c.String())
	})
	t.Run("Nil Pointer Receiver - MarshalJSON", func(t *testing.T) {
		var c *DNSClass
		data, err := c.MarshalJSON()
		require.Error(t, err)
		require.Empty(t, data)
	})

	t.Run("Nil Pointer Receiver - UnmarshalJSON", func(t *testing.T) {
		var c *DNSClass
		err := c.UnmarshalJSON([]byte{})
		require.Error(t, err)
	})
}

func TestDNSRData_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		rdata   DNSRData
		want    []byte
		wantErr bool
	}{
		{
			"empty",
			DNSRData{},
			[]byte{},
			false,
		},
		{
			"SOA",
			DNSRData{nil, dnsSOA},
			[]byte(fmt.Sprintf("\"%s\"", decodedDNSSOA)), false,
		},
		{
			"SRV",
			DNSRData{nil, dnsSRV},
			[]byte(fmt.Sprintf("\"%s\"", decodedDNSSRV)), false,
		},
		{
			"MX",
			DNSRData{nil, dnsMX},
			[]byte(fmt.Sprintf("\"%s\"", decodedDNSMX)), false,
		},
		{
			"IP",
			DNSRData{nil, net.ParseIP("1.2.3.4")},
			[]byte(`"1.2.3.4"`), false,
		},
		{
			"Encoded string",
			DNSRData{nil, []byte(`"any"`)},
			[]byte(`"ImFueSI="`), false,
		},
		{
			"TXT",
			DNSRData{nil, [][]byte{[]byte(`foo`), []byte(`bar`)}},
			[]byte(`"foobar"`), false,
		},
		{
			"Hostname",
			DNSRData{nil, "ns1.tigera.io."},
			[]byte(`"ns1.tigera.io."`), false,
		},
		{
			"Any data",
			DNSRData{nil, 1},
			[]byte(`"1"`), false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.rdata.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func TestDNSRData_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    DNSRData
		wantErr bool
	}{
		{
			"SOA",
			[]byte(fmt.Sprintf("\"%s\"", decodedDNSSOA)),
			DNSRData{[]byte(decodedDNSSOA), dnsSOA},
			false,
		},
		{
			"SRV",
			[]byte(fmt.Sprintf("\"%s\"", decodedDNSSRV)),
			DNSRData{[]byte(decodedDNSSRV), dnsSRV},
			false,
		},
		{
			"MX",
			[]byte(fmt.Sprintf("\"%s\"", decodedDNSMX)),
			DNSRData{[]byte(decodedDNSMX), dnsMX},
			false,
		},
		{
			"Any string",
			[]byte(`"any"`),
			DNSRData{[]byte(`any`), "any"},
			false,
		},
		{
			"malformed SOA - serial",
			[]byte(`"tigera.io. root.tigera.io. #!123 3600 60 86400 1800"`),
			DNSRData{},
			true,
		},
		{
			"malformed SOA - refresh",
			[]byte(`"tigera.io. root.tigera.io. 1 #!123 60 86400 1800"`),
			DNSRData{},
			true,
		},
		{
			"malformed SOA - retry",
			[]byte(`"tigera.io. root.tigera.io. 1 3600 #!123 86400 1800"`),
			DNSRData{},
			true,
		},
		{
			"malformed SOA - expire",
			[]byte(`"tigera.io. root.tigera.io. 1 3600 60 #!123 1800"`),
			DNSRData{},
			true,
		},
		{
			"malformed SOA - minimum",
			[]byte(`"tigera.io. root.tigera.io. 1 3600 60 86400 #!123"`),
			DNSRData{},
			true,
		},
		{
			"malformed SRV - priority",
			[]byte(`"#!123 20 53 ns.tigera.io."`),
			DNSRData{},
			true,
		},
		{
			"malformed SRV - weight",
			[]byte(`"10 #!123 53 ns.tigera.io."`),
			DNSRData{},
			true,
		},
		{
			"malformed SRV - port",
			[]byte(`"10 20 #!123 ns.tigera.io."`),
			DNSRData{},
			true,
		},
		{
			"malformed MX - preference",
			[]byte(`"#!123 mail.tigera.io."`),
			DNSRData{},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dnsRData := DNSRData{}
			err := dnsRData.UnmarshalJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				require.NoError(t, err)
				if !reflect.DeepEqual(dnsRData, tt.want) {
					switch v := dnsRData.Decoded.(type) {
					case layers.DNSSOA:
						logrus.Infof("%s %s %d %d %d %d %d", v.MName, v.RName, v.Serial, v.Refresh, v.Retry, v.Expire, v.Minimum)
					case layers.DNSSRV:
						logrus.Infof("%d %d %d %s", v.Priority, v.Weight, v.Port, v.Name)
					case layers.DNSMX:
						logrus.Infof("%d %s", v.Preference, v.Name)
					default:
						logrus.Infof("%v", v)
					}
					t.Errorf("UnmarshalJSON() got = %+v, want %+v", dnsRData, tt.want)
				}
			}
		})
	}
}

func TestDNSData_NilPointerReceiver(t *testing.T) {
	t.Run("Nil Pointer Receiver - String()", func(t *testing.T) {
		var c *DNSRData
		require.Empty(t, c.String())
	})
	t.Run("Nil Pointer Receiver - MarshalJSON", func(t *testing.T) {
		var c *DNSRData
		data, err := c.MarshalJSON()
		require.Error(t, err)
		require.Empty(t, data)
	})

	t.Run("Nil Pointer Receiver - UnmarshalJSON", func(t *testing.T) {
		var c *DNSRData
		err := c.UnmarshalJSON([]byte{})
		require.Error(t, err)
	})
}

func TestDNSRRSets_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		d       DNSRRSets
		want    []byte
		wantErr bool
	}{
		{
			"empty RRSets",
			DNSRRSets{DNSName{}: DNSRDatas{{}}},
			[]byte(`[{"name":"","class":0,"type":0,"rdata":[]}]`), false,
		},
		{
			"multiple RData per key",
			DNSRRSets{DNSName{
				Name:  "any",
				Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA),
			}: DNSRDatas{{Decoded: net.ParseIP("1.2.3.4")}, {Decoded: net.ParseIP("1.2.3.5")}}},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["1.2.3.4","1.2.3.5"]}]`), false,
		},
		{
			"marshall RRSets with SOA",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, dnsSOA},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["tigera.io. root.tigera.io. 1 3600 60 86400 1800"]}]`), false,
		},

		{
			"marshall RRSets with SRV",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, dnsSRV},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["10 20 53 ns.tigera.io."]}]`), false,
		},

		{
			"marshall RRSets with MX",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, dnsMX},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["10 mail.tigera.io."]}]`), false,
		},

		{
			"marshall RRSets with IP",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, net.ParseIP("1.2.3.4")},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["1.2.3.4"]}]`), false,
		},

		{
			"marshall RRSets with TXT",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, [][]byte{[]byte("foo"), []byte("bar")}},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["foobar"]}]`), false,
		},

		{
			"marshall RRSets with hostname as string",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, "ns1.tigera.io."},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["ns1.tigera.io."]}]`), false,
		},

		{
			"marshall RRSets with bytes",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, []byte("foo")},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["Zm9v"]}]`), false,
		},

		{
			"marshall RRSets with any data",
			DNSRRSets{
				DNSName{Name: "any", Class: DNSClass(layers.DNSClassAny), Type: DNSType(layers.DNSTypeA)}: DNSRDatas{
					{nil, 1},
				},
			},
			[]byte(`[{"name":"any","class":"Any","type":"A","rdata":["1"]}]`), false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.d.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func TestDNSRRSets_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    DNSRRSets
		wantErr bool
	}{
		{
			"Missing key", []byte(`["rdata":["1.2.3.4"]}]`),
			DNSRRSets{},
			true,
		},
		{
			"Missing class", []byte(`[{"name":"any","type":"A","rdata":["1.2.3.4"]}]`),
			DNSRRSets{},
			true,
		},

		{
			"Missing type", []byte(`[{"name":"any","class":"Any","rdata":["1.2.3.4"]}]`),
			DNSRRSets{},
			true,
		},

		{
			"Missing name", []byte(`[{"class":"Any","type":"A",""rdata":["1.2.3.4"]}]`),
			DNSRRSets{},
			true,
		},
		{
			"IP", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["1.2.3.4"]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte("1.2.3.4"), Decoded: "1.2.3.4"}},
			},
			false,
		},
		{
			"Bytes", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["foobar"]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte("foobar"), Decoded: "foobar"}},
			},
			false,
		},
		{
			"MX", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["10 mail.tigera.io."]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte(decodedDNSMX), Decoded: dnsMX}},
			},
			false,
		},
		{
			"SRV", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["10 20 53 ns.tigera.io."]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte(decodedDNSSRV), Decoded: dnsSRV}},
			},
			false,
		},
		{
			"SOA", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["tigera.io. root.tigera.io. 1 3600 60 86400 1800"]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte(decodedDNSSOA), Decoded: dnsSOA}},
			},
			false,
		},
		{
			"TXT", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["foobar"]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte("foobar"), Decoded: "foobar"}},
			},
			false,
		},
		{
			"STR", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["ns1.tigera.io."]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte("ns1.tigera.io."), Decoded: "ns1.tigera.io."}},
			},
			false,
		},
		{
			"BYTES", []byte(`[{"name":"any","class":"Any","type":"A","rdata":["Zm9v"]}]`),
			DNSRRSets{
				DNSName{
					Name:  "any",
					Class: DNSClass(layers.DNSClassAny),
					Type:  DNSType(layers.DNSTypeA),
				}: DNSRDatas{{Raw: []byte("Zm9v"), Decoded: "Zm9v"}},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rrSets := DNSRRSets{}
			err := rrSets.UnmarshalJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				require.NoError(t, err)
				if !reflect.DeepEqual(rrSets, tt.want) {
					t.Errorf("UnmarshalJSON() got = %+v, want %+v", rrSets, tt.want)
				}
			}
		})
	}
}

func TestDNSRRSets_NilPointerReceiver(t *testing.T) {
	t.Run("Nil Pointer Receiver - String()", func(t *testing.T) {
		var c *DNSRRSets
		require.Empty(t, c.String())
	})
	t.Run("Nil Pointer Receiver - MarshalJSON", func(t *testing.T) {
		var c *DNSRRSets
		data, err := c.MarshalJSON()
		require.Error(t, err)
		require.Empty(t, data)
	})

	t.Run("Nil Pointer Receiver - UnmarshalJSON", func(t *testing.T) {
		var c *DNSRRSets
		err := c.UnmarshalJSON([]byte{})
		require.Error(t, err)
	})
}

func TestDNSResponseCode_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		d       DNSResponseCode
		want    []byte
		wantErr bool
	}{
		{"NoError", DNSResponseCode(layers.DNSResponseCodeNoErr), []byte("\"NoError\""), false},
		{"FormErr", DNSResponseCode(layers.DNSResponseCodeFormErr), []byte("\"FormErr\""), false},
		{"ServFail", DNSResponseCode(layers.DNSResponseCodeServFail), []byte("\"ServFail\""), false},
		{"NXDomain", DNSResponseCode(layers.DNSResponseCodeNXDomain), []byte("\"NXDomain\""), false},
		{"NotImp", DNSResponseCode(layers.DNSResponseCodeNotImp), []byte("\"NotImp\""), false},
		{"Refused", DNSResponseCode(layers.DNSResponseCodeRefused), []byte("\"Refused\""), false},
		{"YXDomain", DNSResponseCode(layers.DNSResponseCodeYXDomain), []byte("\"YXDomain\""), false},
		{"NXRRSet", DNSResponseCode(layers.DNSResponseCodeNXRRSet), []byte("\"NXRRSet\""), false},
		{"NotAuth", DNSResponseCode(layers.DNSResponseCodeNotAuth), []byte("\"NotAuth\""), false},
		{"NotZone", DNSResponseCode(layers.DNSResponseCodeNotZone), []byte("\"NotZone\""), false},
		{"DSOTYPENI", DNSResponseCode(11), []byte("\"DSOTYPENI\""), false},
		// These values below are overlapping in layers.DNSResponseCode
		{"BadVers", DNSResponseCode(layers.DNSResponseCodeBadVers), []byte("\"BADSIG\""), false},
		{"BadSig", DNSResponseCode(layers.DNSResponseCodeBadSig), []byte("\"BADSIG\""), false},
		{"BadKey", DNSResponseCode(17), []byte("\"BADKEY\""), false},
		{"BadTime", DNSResponseCode(layers.DNSResponseCodeBadTime), []byte("\"BADTIME\""), false},
		{"BadMode", DNSResponseCode(layers.DNSResponseCodeBadMode), []byte("\"BADMODE\""), false},
		{"BadName", DNSResponseCode(layers.DNSResponseCodeBadName), []byte("\"BADNAME\""), false},
		{"BadAlg", DNSResponseCode(layers.DNSResponseCodeBadAlg), []byte("\"BADALG\""), false},
		{"BadTrunc", DNSResponseCode(layers.DNSResponseCodeBadTruc), []byte("\"BADTRUNC\""), false},
		{"BadCookie", DNSResponseCode(layers.DNSResponseCodeBadCookie), []byte("\"BADCOOKIE\""), false},
		{"Not mapped value", DNSResponseCode(12), []byte(`"#12"`), false},
		{"Max value", DNSResponseCode(255), []byte(`"#255"`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.d.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func TestDNSResponseCode_String(t *testing.T) {
	tests := []struct {
		name string
		d    DNSResponseCode
		want string
	}{
		{"NoError", DNSResponseCode(layers.DNSResponseCodeNoErr), "NoError"},
		{"FormErr", DNSResponseCode(layers.DNSResponseCodeFormErr), "FormErr"},
		{"ServFail", DNSResponseCode(layers.DNSResponseCodeServFail), "ServFail"},
		{"NXDomain", DNSResponseCode(layers.DNSResponseCodeNXDomain), "NXDomain"},
		{"NotImp", DNSResponseCode(layers.DNSResponseCodeNotImp), "NotImp"},
		{"Refused", DNSResponseCode(layers.DNSResponseCodeRefused), "Refused"},
		{"YXDomain", DNSResponseCode(layers.DNSResponseCodeYXDomain), "YXDomain"},
		{"NXRRSet", DNSResponseCode(layers.DNSResponseCodeNXRRSet), "NXRRSet"},
		{"NotAuth", DNSResponseCode(layers.DNSResponseCodeNotAuth), "NotAuth"},
		{"NotZone", DNSResponseCode(layers.DNSResponseCodeNotZone), "NotZone"},
		{"DSOTYPENI", DNSResponseCode(11), "DSOTYPENI"},
		// These values below are overlapping in layers.DNSResponseCode
		{"BadVers", DNSResponseCode(layers.DNSResponseCodeBadVers), "BADSIG"},
		{"BadSig", DNSResponseCode(layers.DNSResponseCodeBadSig), "BADSIG"},
		{"BadKey", DNSResponseCode(17), "BADKEY"},
		{"BadTime", DNSResponseCode(layers.DNSResponseCodeBadTime), "BADTIME"},
		{"BadMode", DNSResponseCode(layers.DNSResponseCodeBadMode), "BADMODE"},
		{"BadName", DNSResponseCode(layers.DNSResponseCodeBadName), "BADNAME"},
		{"BadAlg", DNSResponseCode(layers.DNSResponseCodeBadAlg), "BADALG"},
		{"BadTrunc", DNSResponseCode(layers.DNSResponseCodeBadTruc), "BADTRUNC"},
		{"BadCookie", DNSResponseCode(layers.DNSResponseCodeBadCookie), "BADCOOKIE"},
		{"Not mapped value", DNSResponseCode(12), "#12"},
		{"Max value", DNSResponseCode(255), "#255"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDNSResponseCode_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    DNSResponseCode
		wantErr bool
	}{
		{"NoError", []byte("\"NoError\""), DNSResponseCode(layers.DNSResponseCodeNoErr), false},
		{"FormErr", []byte("\"FormErr\""), DNSResponseCode(layers.DNSResponseCodeFormErr), false},
		{"ServFail", []byte("\"ServFail\""), DNSResponseCode(layers.DNSResponseCodeServFail), false},
		{"NXDomain", []byte("\"NXDomain\""), DNSResponseCode(layers.DNSResponseCodeNXDomain), false},
		{"NotImp", []byte("\"NotImp\""), DNSResponseCode(layers.DNSResponseCodeNotImp), false},
		{"Refused", []byte("\"Refused\""), DNSResponseCode(layers.DNSResponseCodeRefused), false},
		{"YXDomain", []byte("\"YXDomain\""), DNSResponseCode(layers.DNSResponseCodeYXDomain), false},
		{"NXRRSet", []byte("\"NXRRSet\""), DNSResponseCode(layers.DNSResponseCodeNXRRSet), false},
		{"NotAuth", []byte("\"NotAuth\""), DNSResponseCode(layers.DNSResponseCodeNotAuth), false},
		{"NotZone", []byte("\"NotZone\""), DNSResponseCode(layers.DNSResponseCodeNotZone), false},
		{"DSOTYPENI", []byte("\"DSOTYPENI\""), DNSResponseCode(11), false},
		{"BadSig", []byte("\"BADSIG\""), DNSResponseCode(layers.DNSResponseCodeBadSig), false},
		{"BadKey", []byte("\"BADKEY\""), DNSResponseCode(17), false},
		{"BadTime", []byte("\"BADTIME\""), DNSResponseCode(layers.DNSResponseCodeBadTime), false},
		{"BadMode", []byte("\"BADMODE\""), DNSResponseCode(layers.DNSResponseCodeBadMode), false},
		{"BadName", []byte("\"BADNAME\""), DNSResponseCode(layers.DNSResponseCodeBadName), false},
		{"BadAlg", []byte("\"BADALG\""), DNSResponseCode(layers.DNSResponseCodeBadAlg), false},
		{"BadTrunc", []byte("\"BADTRUNC\""), DNSResponseCode(layers.DNSResponseCodeBadTruc), false},
		{"BadCookie", []byte("\"BADCOOKIE\""), DNSResponseCode(layers.DNSResponseCodeBadCookie), false},
		{"Unmapped value", []byte(`"#12"`), DNSResponseCode(12), false},
		{"Any integer", []byte("12"), DNSResponseCode(255), true},
		{"Any string", []byte("\"Any\""), DNSResponseCode(255), true},
		{"Unknown", []byte("Unknown"), DNSResponseCode(255), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dnsResponseCode := DNSResponseCode(layers.DNSResponseCodeNoErr)
			err := dnsResponseCode.UnmarshalJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				require.NoError(t, err)
				if !reflect.DeepEqual(dnsResponseCode, tt.want) {
					t.Errorf("UnmarshalJSON() got = %d, want %d", dnsResponseCode, tt.want)
				}
			}
		})
	}
}

func TestDNSResponseCode_NilPointerReceiver(t *testing.T) {
	t.Run("Nil Pointer Receiver - String()", func(t *testing.T) {
		var c *DNSResponseCode
		require.Empty(t, c.String())
	})
	t.Run("Nil Pointer Receiver - MarshalJSON", func(t *testing.T) {
		var c *DNSResponseCode
		data, err := c.MarshalJSON()
		require.Error(t, err)
		require.Empty(t, data)
	})

	t.Run("Nil Pointer Receiver - UnmarshalJSON", func(t *testing.T) {
		var c *DNSResponseCode
		err := c.UnmarshalJSON([]byte{})
		require.Error(t, err)
	})
}

func TestDNSServer_MarshalJSON(t *testing.T) {
	type fields struct {
		Endpoint Endpoint
		IP       net.IP
		Labels   map[string]string
	}
	tests := []struct {
		name    string
		fields  fields
		want    []byte
		wantErr bool
	}{
		{
			"any endpoint",
			fields{
				Endpoint: Endpoint{"wep", "e", "e-*", "ns", 0},
				IP:       net.ParseIP("1.2.3.4"),
			},
			[]byte(`{"name":"e","name_aggr":"e-*","namespace":"ns","ip":"1.2.3.4"}`), false,
		},
		{
			"any endpoint with labels",
			fields{
				Endpoint: Endpoint{"wep", "e", "e-*", "ns", 0},
				IP:       net.ParseIP("1.2.3.4"),
				Labels:   map[string]string{"key": "value"},
			},
			[]byte(`{"name":"e","name_aggr":"e-*","namespace":"ns","ip":"1.2.3.4"}`), false,
		},
		{
			"empty json",
			fields{},
			[]byte(`{"name":"","name_aggr":"","namespace":"","ip":""}`), false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DNSServer{
				Endpoint: tt.fields.Endpoint,
				IP:       tt.fields.IP,
				Labels:   tt.fields.Labels,
			}
			got, err := d.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func TestDNSServer_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    *DNSServer
		wantErr bool
	}{
		{
			"unmarshall dns server",
			[]byte(`{"name":"e","name_aggr":"e-*","namespace":"ns","ip":"1.2.3.4"}`),
			&DNSServer{
				Endpoint: Endpoint{Name: "e", AggregatedName: "e-*", Namespace: "ns"},
				IP:       net.ParseIP("1.2.3.4"),
			},
			false,
		},
		{
			"empty fields",
			[]byte(`{"name":"","name_aggr":"","namespace":"","ip":""}`),
			&DNSServer{},
			false,
		},
		{
			"empty string",
			[]byte(``),
			&DNSServer{},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DNSServer{}
			err := d.UnmarshalJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				require.NoError(t, err)
				if !reflect.DeepEqual(d, tt.want) {
					t.Errorf("UnmarshalJSON() got = %+v, want %+v", d, tt.want)
				}
			}
		})
	}
}

func TestDNSServer_NilPointerReceiver(t *testing.T) {
	t.Run("Nil Pointer Receiver - MarshalJSON", func(t *testing.T) {
		var c *DNSServer
		data, err := c.MarshalJSON()
		require.Error(t, err)
		require.Empty(t, data)
	})

	t.Run("Nil Pointer Receiver - UnmarshalJSON", func(t *testing.T) {
		var c *DNSServer
		err := c.UnmarshalJSON([]byte{})
		require.Error(t, err)
	})
}

func TestDNSType_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		d       DNSType
		want    []byte
		wantErr bool
	}{
		{"A", DNSType(layers.DNSTypeA), []byte("\"A\""), false},
		{"NS", DNSType(layers.DNSTypeNS), []byte("\"NS\""), false},
		{"MD", DNSType(layers.DNSTypeMD), []byte("\"MD\""), false},
		{"MF", DNSType(layers.DNSTypeMF), []byte("\"MF\""), false},
		{"CNAME", DNSType(layers.DNSTypeCNAME), []byte("\"CNAME\""), false},
		{"SOA", DNSType(layers.DNSTypeSOA), []byte("\"SOA\""), false},
		{"MB", DNSType(layers.DNSTypeMB), []byte("\"MB\""), false},
		{"MG", DNSType(layers.DNSTypeMG), []byte("\"MG\""), false},
		{"MR", DNSType(layers.DNSTypeMR), []byte("\"MR\""), false},
		{"NULL", DNSType(layers.DNSTypeNULL), []byte("\"NULL\""), false},
		{"WKS", DNSType(layers.DNSTypeWKS), []byte("\"WKS\""), false},
		{"PTR", DNSType(layers.DNSTypePTR), []byte("\"PTR\""), false},
		{"HINFO", DNSType(layers.DNSTypeHINFO), []byte("\"HINFO\""), false},
		{"MINFO", DNSType(layers.DNSTypeMINFO), []byte("\"MINFO\""), false},
		{"MX", DNSType(layers.DNSTypeMX), []byte("\"MX\""), false},
		{"TXT", DNSType(layers.DNSTypeTXT), []byte("\"TXT\""), false},
		{"AAAA", DNSType(layers.DNSTypeAAAA), []byte("\"AAAA\""), false},
		{"SRV", DNSType(layers.DNSTypeSRV), []byte("\"SRV\""), false},
		{"OPT", DNSType(layers.DNSTypeOPT), []byte("\"OPT\""), false},
		{"URI", DNSType(layers.DNSTypeURI), []byte("\"URI\""), false},
		{"Unmapped value", DNSType(50), []byte("\"#50\""), false},
		{"Max value", DNSType(65535), []byte("\"#65535\""), false},
		{"Min value", DNSType(0), []byte("\"#0\""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.d.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func TestDNSType_String(t *testing.T) {
	tests := []struct {
		name string
		d    DNSType
		want string
	}{
		{"A", DNSType(layers.DNSTypeA), "A"},
		{"NS", DNSType(layers.DNSTypeNS), "NS"},
		{"MD", DNSType(layers.DNSTypeMD), "MD"},
		{"MF", DNSType(layers.DNSTypeMF), "MF"},
		{"CNAME", DNSType(layers.DNSTypeCNAME), "CNAME"},
		{"SOA", DNSType(layers.DNSTypeSOA), "SOA"},
		{"MB", DNSType(layers.DNSTypeMB), "MB"},
		{"MG", DNSType(layers.DNSTypeMG), "MG"},
		{"MR", DNSType(layers.DNSTypeMR), "MR"},
		{"NULL", DNSType(layers.DNSTypeNULL), "NULL"},
		{"WKS", DNSType(layers.DNSTypeWKS), "WKS"},
		{"PTR", DNSType(layers.DNSTypePTR), "PTR"},
		{"HINFO", DNSType(layers.DNSTypeHINFO), "HINFO"},
		{"MINFO", DNSType(layers.DNSTypeMINFO), "MINFO"},
		{"MX", DNSType(layers.DNSTypeMX), "MX"},
		{"TXT", DNSType(layers.DNSTypeTXT), "TXT"},
		{"AAAA", DNSType(layers.DNSTypeAAAA), "AAAA"},
		{"SRV", DNSType(layers.DNSTypeSRV), "SRV"},
		{"OPT", DNSType(layers.DNSTypeOPT), "OPT"},
		{"URI", DNSType(layers.DNSTypeURI), "URI"},
		{"Unmapped value", DNSType(50), "#50"},
		{"Max value", DNSType(65535), "#65535"},
		{"Min value", DNSType(0), "#0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDNSType_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    DNSType
		wantErr bool
	}{
		{"A", []byte("\"A\""), DNSType(layers.DNSTypeA), false},
		{"NS", []byte("\"NS\""), DNSType(layers.DNSTypeNS), false},
		{"MD", []byte("\"MD\""), DNSType(layers.DNSTypeMD), false},
		{"MF", []byte("\"MF\""), DNSType(layers.DNSTypeMF), false},
		{"CNAME", []byte("\"CNAME\""), DNSType(layers.DNSTypeCNAME), false},
		{"SOA", []byte("\"SOA\""), DNSType(layers.DNSTypeSOA), false},
		{"MB", []byte("\"MB\""), DNSType(layers.DNSTypeMB), false},
		{"MG", []byte("\"MG\""), DNSType(layers.DNSTypeMG), false},
		{"MR", []byte("\"MR\""), DNSType(layers.DNSTypeMR), false},
		{"NULL", []byte("\"NULL\""), DNSType(layers.DNSTypeNULL), false},
		{"WKS", []byte("\"WKS\""), DNSType(layers.DNSTypeWKS), false},
		{"PTR", []byte("\"PTR\""), DNSType(layers.DNSTypePTR), false},
		{"HINFO", []byte("\"HINFO\""), DNSType(layers.DNSTypeHINFO), false},
		{"MINFO", []byte("\"MINFO\""), DNSType(layers.DNSTypeMINFO), false},
		{"MX", []byte("\"MX\""), DNSType(layers.DNSTypeMX), false},
		{"TXT", []byte("\"TXT\""), DNSType(layers.DNSTypeTXT), false},
		{"AAAA", []byte("\"AAAA\""), DNSType(layers.DNSTypeAAAA), false},
		{"SRV", []byte("\"SRV\""), DNSType(layers.DNSTypeSRV), false},
		{"OPT", []byte("\"OPT\""), DNSType(layers.DNSTypeOPT), false},
		{"URI", []byte("\"URI\""), DNSType(layers.DNSTypeURI), false},
		{"Unmapped value", []byte("\"#60\""), DNSType(60), false},
		{"Any string", []byte("\"Any\""), DNSType(0), false},
		{"Any integer", []byte("10"), DNSType(0), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dnsType := DNSType(50)
			err := dnsType.UnmarshalJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				require.NoError(t, err)
				if !reflect.DeepEqual(dnsType, tt.want) {
					t.Errorf("UnmarshalJSON() got = %d, want %d", dnsType, tt.want)
				}
			}
		})
	}
}

func TestDNSType_NilPointerReceiver(t *testing.T) {
	t.Run("Nil Pointer Receiver - String()", func(t *testing.T) {
		var c *DNSType
		require.Empty(t, c.String())
	})
	t.Run("Nil Pointer Receiver - MarshalJSON", func(t *testing.T) {
		var c *DNSType
		data, err := c.MarshalJSON()
		require.Error(t, err)
		require.Empty(t, data)
	})

	t.Run("Nil Pointer Receiver - UnmarshalJSON", func(t *testing.T) {
		var c *DNSType
		err := c.UnmarshalJSON([]byte{})
		require.Error(t, err)
	})
}

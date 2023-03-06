// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package v1

import (
	"bytes"
	"encoding/base64"
	gojson "encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/json"

	"github.com/google/gopacket/layers"
	"golang.org/x/net/idna"
)

// DNSLogParams define querying parameters to retrieve DNS logs
type DNSLogParams struct {
	QueryParams        `json:",inline" validate:"required"`
	LogSelectionParams `json:",inline"`
}

type DNSAggregationParams struct {
	// Inherit all the normal DNS log selection parameters.
	DNSLogParams `json:",inline"`
	Aggregations map[string]gojson.RawMessage `json:"aggregations"`
	NumBuckets   int                          `json:"num_buckets"`
}

type DNSLog struct {
	StartTime       time.Time         `json:"start_time"`
	EndTime         time.Time         `json:"end_time"`
	Type            DNSLogType        `json:"type"`
	Count           uint              `json:"count"`
	ClientName      string            `json:"client_name"`
	ClientNameAggr  string            `json:"client_name_aggr"`
	ClientNamespace string            `json:"client_namespace"`
	ClientIP        *net.IP           `json:"client_ip"`
	ClientLabels    map[string]string `json:"client_labels"`
	Servers         []DNSServer       `json:"servers"`
	QName           QName             `json:"qname"`
	QClass          DNSClass          `json:"qclass"`
	QType           DNSType           `json:"qtype"`
	RCode           DNSResponseCode   `json:"rcode"`
	RRSets          DNSRRSets         `json:"rrsets"`
	Latency         DNSLatency        `json:"latency"`
	LatencyCount    int               `json:"latency_count"`
	LatencyMean     time.Duration     `json:"latency_mean"`
	LatencyMax      time.Duration     `json:"latency_max"`
	Host            string            `json:"host"`
}

type DNSLatency struct {
	// Number of successful latency measurements contributing to
	// the following mean and max.
	Count int `json:"count"`

	// Mean latency.
	Mean time.Duration `json:"mean"`

	// Max latency.
	Max time.Duration `json:"max"`
}

type DNSName struct {
	Name  string
	Class DNSClass
	Type  DNSType
}

type dnsNameEncoded struct {
	Name  string      `json:"name"`
	Class interface{} `json:"class"`
	Type  interface{} `json:"type"`
}

func (d DNSName) MarshalJSON() ([]byte, error) {
	n := d.encodeDNSName()

	return json.Marshal(&n)
}

func (d DNSName) encodeDNSName() dnsNameEncoded {
	n := dnsNameEncoded{
		aNameToUName(d.Name),
		layers.DNSClass(d.Class).String(),
		layers.DNSType(d.Type).String(),
	}
	if n.Class == "Unknown" {
		n.Class = uint(d.Class)
	}
	if n.Type == "Unknown" {
		n.Type = uint(d.Type)
	}
	return n
}

func (d DNSName) String() string {
	return fmt.Sprintf("%s %s %s", d.Name, d.Class.String(), d.Type.String())
}

func (a DNSName) Less(b DNSName) bool {
	reverse := func(s []string) []string {
		for i := 0; i < len(s)/2; i++ {
			j := len(s) - i - 1
			s[i], s[j] = s[j], s[i]
		}
		return s
	}

	l := strings.Join(reverse(strings.Split(a.Name, ".")), ".")
	r := strings.Join(reverse(strings.Split(b.Name, ".")), ".")

	c := strings.Compare(l, r)
	switch {
	case c < 0:
		return true
	case c > 0:
		return false
	}

	switch {
	case a.Class < b.Class:
		return true
	case a.Class > b.Class:
		return false
	}

	return a.Type < b.Type
}

type DNSResponseCode layers.DNSResponseCode

func (d *DNSResponseCode) String() string {
	if d == nil {
		return ""
	}
	if res, ok := dnsResponseCodeTable[*d]; ok {
		return res
	}

	return fmt.Sprintf("#%d", *d)
}

func (d *DNSResponseCode) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte{}, fmt.Errorf("cannot marshal nil value into JSON")
	}
	if res, ok := dnsResponseCodeTable[*d]; ok {
		return json.Marshal(&res)
	}

	return json.Marshal(fmt.Sprintf("#%d", *d))
}

func (d *DNSResponseCode) UnmarshalJSON(data []byte) error {
	var item string
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}

	if strings.HasPrefix(item, "#") {
		code, err := strconv.Atoi(strings.TrimPrefix(item, "#"))
		if err != nil {
			return fmt.Errorf("failed to recognize DNS response code %s", item)
		}

		*d = DNSResponseCode(code)

		return nil
	}

	switch strings.ToLower(item) {
	case "noerror":
		*d = DNSResponseCode(layers.DNSResponseCodeNoErr)
	case "formerr":
		*d = DNSResponseCode(layers.DNSResponseCodeFormErr)
	case "servfail":
		*d = DNSResponseCode(layers.DNSResponseCodeServFail)
	case "nxdomain":
		*d = DNSResponseCode(layers.DNSResponseCodeNXDomain)
	case "notimp":
		*d = DNSResponseCode(layers.DNSResponseCodeNotImp)
	case "refused":
		*d = DNSResponseCode(layers.DNSResponseCodeRefused)
	case "yxdomain":
		*d = DNSResponseCode(layers.DNSResponseCodeYXDomain)
	case "yxrrset":
		*d = DNSResponseCode(layers.DNSResponseCodeYXRRSet)
	case "nxrrset":
		*d = DNSResponseCode(layers.DNSResponseCodeNXRRSet)
	case "notauth":
		*d = DNSResponseCode(layers.DNSResponseCodeNotAuth)
	case "notzone":
		*d = DNSResponseCode(layers.DNSResponseCodeNotZone)
	case "dsotypeni":
		*d = DNSResponseCode(11)
	case "badsig":
		*d = DNSResponseCode(layers.DNSResponseCodeBadVers)
	case "badkey":
		*d = DNSResponseCode(layers.DNSResponseCodeBadKey)
	case "badtime":
		*d = DNSResponseCode(layers.DNSResponseCodeBadTime)
	case "badmode":
		*d = DNSResponseCode(layers.DNSResponseCodeBadMode)
	case "badname":
		*d = DNSResponseCode(layers.DNSResponseCodeBadName)
	case "badalg":
		*d = DNSResponseCode(layers.DNSResponseCodeBadAlg)
	case "badtrunc":
		*d = DNSResponseCode(layers.DNSResponseCodeBadTruc)
	case "badcookie":
		*d = DNSResponseCode(layers.DNSResponseCodeBadCookie)
	default:
		return fmt.Errorf("failed to recognize DNS response code %s", item)
	}

	return nil
}

// Formatting from IANA DNS Parameters
var dnsResponseCodeTable = map[DNSResponseCode]string{
	0:  "NoError",
	1:  "FormErr",
	2:  "ServFail",
	3:  "NXDomain",
	4:  "NotImp",
	5:  "Refused",
	6:  "YXDomain",
	7:  "YXRRSet",
	8:  "NXRRSet",
	9:  "NotAuth",
	10: "NotZone",
	11: "DSOTYPENI",
	16: "BADSIG",
	17: "BADKEY",
	18: "BADTIME",
	19: "BADMODE",
	20: "BADNAME",
	21: "BADALG",
	22: "BADTRUNC",
	23: "BADCOOKIE",
}

type DNSClass layers.DNSClass

func (d *DNSClass) String() string {
	if d == nil {
		return ""
	}
	c := layers.DNSClass(*d).String()
	if c != "Unknown" {
		return c
	}
	return fmt.Sprintf("#%d", *d)
}

func (d *DNSClass) MarshalJSON() ([]byte, error) {
	if d != nil {
		s := d.String()
		return json.Marshal(&s)
	}

	return []byte{}, fmt.Errorf("cannot marshal nil value into JSON")
}

func (d *DNSClass) UnmarshalJSON(data []byte) error {
	var val string
	if err := json.Unmarshal(data, &val); err != nil {
		return err
	}
	*d = toDNSClass(val)

	return nil
}

func convertToDNSClass(val interface{}) (DNSClass, error) {
	switch v := val.(type) {
	case string:
		return toDNSClass(v), nil
	case int:
		return DNSClass(v), nil
	case float64:
		return DNSClass(v), nil
	default:
		return DNSClass(0), fmt.Errorf("failed to read dns class format")
	}
}

func toDNSClass(val string) DNSClass {
	if strings.HasPrefix(val, "#") {
		code, err := strconv.Atoi(strings.TrimPrefix(val, "#"))
		if err != nil {
			logrus.Warnf("Failed to recognize DNS Class %s. Will default to 0", val)
			return DNSClass(0)
		}

		return DNSClass(code)
	}

	switch strings.ToLower(val) {
	case "in":
		return DNSClass(layers.DNSClassIN)
	case "cs":
		return DNSClass(layers.DNSClassCS)
	case "ch":
		return DNSClass(layers.DNSClassCH)
	case "hs":
		return DNSClass(layers.DNSClassHS)
	case "any":
		return DNSClass(layers.DNSClassAny)
	default:
		logrus.Warnf("Failed to recognize DNS Class %s. Will default to 0", val)
		return DNSClass(0)
	}
}

type DNSType layers.DNSType

func (d *DNSType) String() string {
	if d == nil {
		return ""
	}
	t := layers.DNSType(*d).String()
	if t != "Unknown" {
		return t
	}
	return fmt.Sprintf("#%d", *d)
}

func (d *DNSType) MarshalJSON() ([]byte, error) {
	if d != nil {
		s := d.String()
		return json.Marshal(&s)
	}

	return []byte{}, fmt.Errorf("cannot marshal nil value into JSON")
}

func (d *DNSType) UnmarshalJSON(data []byte) error {
	var val string
	if err := json.Unmarshal(data, &val); err != nil {
		return err
	}

	*d = toDNSType(val)

	return nil
}

func toDNSType(val string) DNSType {
	if strings.HasPrefix(val, "#") {
		code, err := strconv.Atoi(strings.TrimPrefix(val, "#"))
		if err != nil {
			logrus.Warnf("Failed to recognize DNS Type %s. Will default to 0", val)
			return DNSType(0)
		}

		return DNSType(code)
	}

	switch val {
	case "A":
		return DNSType(layers.DNSTypeA)
	case "NS":
		return DNSType(layers.DNSTypeNS)
	case "MD":
		return DNSType(layers.DNSTypeMD)
	case "MF":
		return DNSType(layers.DNSTypeMF)
	case "CNAME":
		return DNSType(layers.DNSTypeCNAME)
	case "SOA":
		return DNSType(layers.DNSTypeSOA)
	case "MB":
		return DNSType(layers.DNSTypeMB)
	case "MG":
		return DNSType(layers.DNSTypeMG)
	case "MR":
		return DNSType(layers.DNSTypeMR)
	case "NULL":
		return DNSType(layers.DNSTypeNULL)
	case "WKS":
		return DNSType(layers.DNSTypeWKS)
	case "PTR":
		return DNSType(layers.DNSTypePTR)
	case "HINFO":
		return DNSType(layers.DNSTypeHINFO)
	case "MINFO":
		return DNSType(layers.DNSTypeMINFO)
	case "MX":
		return DNSType(layers.DNSTypeMX)
	case "TXT":
		return DNSType(layers.DNSTypeTXT)
	case "AAAA":
		return DNSType(layers.DNSTypeAAAA)
	case "SRV":
		return DNSType(layers.DNSTypeSRV)
	case "OPT":
		return DNSType(layers.DNSTypeOPT)
	case "URI":
		return DNSType(layers.DNSTypeURI)
	default:
		logrus.Warnf("Failed to recognize DNS Type %s. Will default to 0", val)
		return DNSType(0)
	}
}

type DNSNames []DNSName

func (d DNSNames) Len() int {
	return len(d)
}

func (d DNSNames) Less(i, j int) bool {
	return d[i].Less(d[j])
}

func (d DNSNames) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

type DNSRRSets map[DNSName]DNSRDatas

func (d *DNSRRSets) String() string {
	if d == nil {
		return ""
	}

	var s []string
	var names DNSNames

	data := *d
	for n := range data {
		names = append(names, n)
	}
	sort.Sort(names)

	for _, n := range names {
		for _, r := range data[n] {
			s = append(s, fmt.Sprintf("%s %s", n.String(), r.String()))
		}
	}
	return strings.Join(s, "\n")
}

// Add inserts a DNSRData into the appropriate DNSRDatas in sorted order
func (d *DNSRRSets) Add(name DNSName, rdata DNSRData) {
	data := *d
	index := sort.Search(len(data[name]), func(i int) bool { return !data[name][i].Less(rdata) })
	data[name] = append(data[name], DNSRData{})
	copy(data[name][index+1:], data[name][index:])
	data[name][index] = rdata
}

type dnsRRSetsEncoded struct {
	dnsNameEncoded
	RData DNSRDatas `json:"rdata"`
}

func (d *DNSRRSets) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte{}, fmt.Errorf("cannot marshal nil value into JSON")
	}
	var r []dnsRRSetsEncoded
	for name, rdatas := range *d {
		r = append(r, dnsRRSetsEncoded{name.encodeDNSName(), rdatas})
	}

	return json.Marshal(r)
}

func (d *DNSRRSets) UnmarshalJSON(data []byte) error {
	dnsRRSetsEncoded := []dnsRRSetsEncoded{}
	err := json.Unmarshal(data, &dnsRRSetsEncoded)
	if err != nil {
		return err
	}

	rrSets := DNSRRSets(make(map[DNSName]DNSRDatas))
	for _, dnsRRSet := range dnsRRSetsEncoded {
		dnsClass, error := convertToDNSClass(dnsRRSet.dnsNameEncoded.Class)
		if error != nil {
			return fmt.Errorf("failed to convert %v to string", dnsRRSet.Class)
		}
		dnsType, ok := dnsRRSet.dnsNameEncoded.Type.(string)
		if !ok {
			return fmt.Errorf("failed to convert %v to DNSType", dnsRRSet.Type)
		}

		dnsName := DNSName{Name: dnsRRSet.dnsNameEncoded.Name, Class: dnsClass, Type: toDNSType(dnsType)}
		for _, rdata := range dnsRRSet.RData {
			rrSets.Add(dnsName, rdata)
		}
	}
	*d = rrSets
	return nil
}

type DNSRDatas []DNSRData

func (d DNSRDatas) Len() int {
	return len(d)
}

func (d DNSRDatas) Less(i, j int) bool {
	return d[i].Less(d[j])
}

func (d DNSRDatas) Swap(i, j int) {
	d[j], d[i] = d[i], d[j]
}

type DNSRData struct {
	Raw     []byte
	Decoded interface{}
}

func (a *DNSRData) Less(b DNSRData) bool {
	return bytes.Compare(a.Raw, b.Raw) < 0
}

func (d *DNSRData) String() string {
	if d == nil {
		return ""
	}

	switch v := d.Decoded.(type) {
	case net.IP:
		return v.String()
	case string:
		return v
	case []byte:
		return base64.StdEncoding.EncodeToString(v)
	case [][]byte:
		// This might not be the right thing to do here. It depends on how gopacket interprets multiple
		// TXT records.
		return string(bytes.Join(v, []byte{}))
	case layers.DNSSOA:
		return fmt.Sprintf("%s %s %d %d %d %d %d", v.MName, v.RName, v.Serial, v.Refresh, v.Retry, v.Expire, v.Minimum)
	case layers.DNSSRV:
		return fmt.Sprintf("%d %d %d %s", v.Priority, v.Weight, v.Port, v.Name)
	case layers.DNSMX:
		return fmt.Sprintf("%d %s", v.Preference, v.Name)
	default:
		return fmt.Sprintf("%#v", d.Decoded)
	}
}

// IDNAString is like String() but decodes international domain names to unicode
func (d *DNSRData) IDNAString() string {
	switch v := d.Decoded.(type) {
	case net.IP:
		return v.String()
	case string:
		return aNameToUName(v)
	case []byte:
		return base64.StdEncoding.EncodeToString(v)
	case [][]byte:
		// This might not be the right thing to do here. It depends on how gopacket interprets multiple
		// TXT records.
		return string(bytes.Join(v, []byte{}))
	case layers.DNSSOA:
		return fmt.Sprintf("%s %s %d %d %d %d %d",
			aNameToUName(string(v.MName)), aNameToUName(string(v.RName)), v.Serial, v.Refresh, v.Retry, v.Expire, v.Minimum)
	case layers.DNSSRV:
		return fmt.Sprintf("%d %d %d %s", v.Priority, v.Weight, v.Port, aNameToUName(string(v.Name)))
	case layers.DNSMX:
		return fmt.Sprintf("%d %s", v.Preference, aNameToUName(string(v.Name)))
	default:
		return fmt.Sprintf("%#v", d.Decoded)
	}
}

func (d *DNSRData) MarshalJSON() ([]byte, error) {
	if d != nil {
		if d.Decoded == nil {
			return []byte{}, nil
		}

		return json.Marshal(d.IDNAString())
	}

	return []byte{}, fmt.Errorf("cannot marshal nil value into JSON")
}

func (d *DNSRData) UnmarshalJSON(data []byte) error {
	var val string
	if err := json.Unmarshal(data, &val); err != nil {
		return err
	}

	tokens := strings.Split(val, " ")
	switch len(tokens) {
	default:
		// During the marshaling, we loose type information as most values
		// come in as string format
		d.Decoded = val
	case 2:
		// Detect an MX record
		mx, err := toMXRecord(tokens)
		if err != nil {
			return err
		}
		d.Decoded = *mx

	case 4:
		// Detect an SRV record
		srv, err := toSRVRecord(tokens)
		if err != nil {
			return err
		}
		d.Decoded = *srv

	case 7:
		// Detected a SOA record
		soa, err := toSOARecord(tokens)
		if err != nil {
			return err
		}
		d.Decoded = *soa
	}

	d.Raw = []byte(val)

	return nil
}

func toMXRecord(tokens []string) (*layers.DNSMX, error) {
	if len(tokens) != 2 {
		return nil, fmt.Errorf("invalid format for DNSMX record")
	}

	preference, err := strconv.Atoi(tokens[0])
	if err != nil {
		return nil, err
	}
	return &layers.DNSMX{
		Preference: uint16(preference),
		Name:       []byte(tokens[1]),
	}, nil
}

func toSRVRecord(tokens []string) (*layers.DNSSRV, error) {
	if len(tokens) != 4 {
		return nil, fmt.Errorf("invalid format for DNSSRV record")
	}

	priority, err := strconv.Atoi(tokens[0])
	if err != nil {
		return nil, err
	}
	weight, err := strconv.Atoi(tokens[1])
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(tokens[2])
	if err != nil {
		return nil, err
	}

	return &layers.DNSSRV{
		Priority: uint16(priority),
		Weight:   uint16(weight),
		Port:     uint16(port),
		Name:     []byte(tokens[3]),
	}, nil
}

func toSOARecord(tokens []string) (*layers.DNSSOA, error) {
	if len(tokens) != 7 {
		return nil, fmt.Errorf("invalid format for DNSSOA record")
	}

	serial, err := strconv.Atoi(tokens[2])
	if err != nil {
		return nil, err
	}
	refresh, err := strconv.Atoi(tokens[3])
	if err != nil {
		return nil, err
	}
	retry, err := strconv.Atoi(tokens[4])
	if err != nil {
		return nil, err
	}
	expire, err := strconv.Atoi(tokens[5])
	if err != nil {
		return nil, err
	}
	minimum, err := strconv.Atoi(tokens[6])
	if err != nil {
		return nil, err
	}
	return &layers.DNSSOA{
		MName:   []byte(tokens[0]),
		RName:   []byte(tokens[1]),
		Serial:  uint32(serial),
		Refresh: uint32(refresh),
		Retry:   uint32(retry),
		Expire:  uint32(expire),
		Minimum: uint32(minimum),
	}, nil
}

type DNSServer struct {
	Endpoint
	IP     net.IP
	Labels map[string]string
}

type dnsServerEncoded struct {
	// Include the fields from Endpoint.
	Name      string `json:"name"`
	NameAggr  string `json:"name_aggr"`
	Namespace string `json:"namespace"`
	// Type is not serialized for DNS servers.

	// As well as any other DNSServer fields.
	IP     string            `json:"ip"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (d *DNSServer) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte{}, fmt.Errorf("cannot marshal nil value into JSON")
	}

	ip := d.IP.String()
	if ip == "<nil>" {
		ip = ""
	}

	return json.Marshal(&dnsServerEncoded{
		Name:      d.Name,
		NameAggr:  d.AggregatedName,
		Namespace: d.Namespace,
		IP:        ip,
		Labels:    d.Labels,
	})
}

func (d *DNSServer) UnmarshalJSON(data []byte) error {
	dnsServerEncoded := dnsServerEncoded{}
	err := json.Unmarshal(data, &dnsServerEncoded)
	if err != nil {
		return err
	}
	d.Name = dnsServerEncoded.Name
	d.AggregatedName = dnsServerEncoded.NameAggr
	d.Namespace = dnsServerEncoded.Namespace
	d.IP = net.ParseIP(dnsServerEncoded.IP)
	d.Labels = dnsServerEncoded.Labels

	return nil
}

type DNSStats struct {
	Count uint `json:"count"`
}

type DNSLogType string

const (
	DNSLogTypeLog      DNSLogType = "log"
	DNSLogTypeUnlogged DNSLogType = "unlogged"
)

type QName string

func (q QName) MarshalJSON() ([]byte, error) {
	u := aNameToUName(string(q))
	return json.Marshal(u)
}

var (
	idnaProfile *idna.Profile
	ipOnce      sync.Once
)

// aNameToUName takes an "A-Name" (ASCII encoded) and converts it to a "U-Name"
// which is its unicode equivalent according to the International Domain Names
// for Applications (IDNA) spec (https://tools.ietf.org/html/rfc5891)
func aNameToUName(aname string) string {
	ipOnce.Do(func() {
		idnaProfile = idna.New()
	})
	u, err := idnaProfile.ToUnicode(aname)
	if err != nil {
		// If there was some problem converting, just return the name as we
		// encountered it in the DNS protocol
		return aname
	}
	return u
}

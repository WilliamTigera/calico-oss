package template

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kelseyhightower/memkv"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/confd/pkg/backends"
)

const (
	maxBIRDSymLen = 64
)

func newFuncMap() map[string]interface{} {
	m := make(map[string]interface{})
	m["base"] = path.Base
	m["split"] = strings.Split
	m["json"] = UnmarshalJsonObject
	m["jsonArray"] = UnmarshalJsonArray
	m["dir"] = path.Dir
	m["map"] = CreateMap
	m["getenv"] = Getenv
	m["join"] = strings.Join
	m["datetime"] = time.Now
	m["toUpper"] = strings.ToUpper
	m["toLower"] = strings.ToLower
	m["contains"] = strings.Contains
	m["replace"] = strings.Replace
	m["hasSuffix"] = strings.HasSuffix
	m["lookupIP"] = LookupIP
	m["lookupSRV"] = LookupSRV
	m["fileExists"] = isFileExist
	m["base64Encode"] = Base64Encode
	m["base64Decode"] = Base64Decode
	m["hashToIPv4"] = hashToIPv4
	m["emitBIRDExternalNetworkConfig"] = EmitBIRDExternalNetworkConfig
	m["emitExternalNetworkTableName"] = EmitExternalNetworkTableName
	m["emitFunctionName"] = EmitBGPFilterFunctionName
	m["emitBIRDBGPFilterFuncs"] = EmitBIRDBGPFilterFuncs
	return m
}

func addFuncs(out, in map[string]interface{}) {
	for name, fn := range in {
		out[name] = fn
	}
}

// EmitExternalNetworkTableName returns a formatted name for use as a BIRD table, truncating and hashing if the provided
// name would result in a table name longer than the max allowable length of 64 chars.
// e.g. input of "my-external-network" would result in output of "'T_my-external-network'"
func EmitExternalNetworkTableName(name string) (string, error) {
	prefix := "T_"
	resizedName, err := truncateAndHashName(name, maxBIRDSymLen-len(prefix))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("'%s%s'", prefix, resizedName), nil
}

// emitBGPFilterStatement produces a single comparison expression to be used within a multi-statement BIRD filter
// function.
// e.g input of ("In", "77.0.0.1/16", "accept") produces output of "if ((net ~ 77.0.0.1/16)) then { accept; }"
func emitBGPFilterStatement(matchOperator, cidr, source, mAction string) (string, error) {
	actionStatement, err := emitFilterAction(mAction)
	if err != nil {
		return "", err
	}

	var filterStatement string
	if cidr != "" {
		if matchOperator == "" {
			return "", fmt.Errorf("Operator not included in BGPFilter")
		}
		matchCIDRStatement, err := emitFilterMatchCIDR(cidr, matchOperator)
		if err != nil {
			return "", err
		}
		filterStatement = fmt.Sprintf("if (%s", matchCIDRStatement)
	}

	if source != "" {
		matchTypeStatment, err := emitFilterMatchSource(source)
		if err != nil {
			return "", nil
		}
		if filterStatement != "" {
			filterStatement = fmt.Sprintf("%s && %s", filterStatement, matchTypeStatment)
		} else {
			filterStatement = fmt.Sprintf("if (%s", matchTypeStatment)
		}
	}

	if filterStatement != "" {
		filterStatement = fmt.Sprintf("%s) then { %s }", filterStatement, actionStatement)
	} else {
		filterStatement = actionStatement
	}
	return filterStatement, nil
}

func emitFilterAction(mAction string) (string, error) {
	action := strings.ToLower(mAction)
	if action != "accept" && action != "reject" {
		return "", fmt.Errorf("Unexpected action found in BGPFilter: %s", mAction)
	}
	return fmt.Sprintf("%s;", action), nil
}

func emitFilterMatchCIDR(cidr, operator string) (string, error) {
	operatorLUT := map[string]string{
		string(v3.Equal): "=",
		v3.NotEqual:      "!=",
		v3.In:            "~",
		v3.NotIn:         "!~",
	}
	op, ok := operatorLUT[operator]
	if !ok {
		return "", fmt.Errorf("Unexpected operator found in BGPFilter: %s", operator)
	}
	return fmt.Sprintf("(net %s %s)", op, cidr), nil
}

func emitFilterMatchSource(source string) (string, error) {
	if source != "RemotePeers" {
		return "", fmt.Errorf("Unexpected source found in BGPFilter: %s", source)
	}
	return "((defined(source)) && (source ~ [ RTS_BGP ]))", nil
}

// EmitBGPFilterFunctionName returns a formatted name for use as a BIRD function, truncating and hashing if the provided
// name would result in a function name longer than the max allowable length of 64 chars.
// e.g. input of ("my-bgp-filter", "import", "4") would result in output of "'bgp_my-bpg-filter_importFilterV4'"
func EmitBGPFilterFunctionName(filterName, direction, version string) (string, error) {
	normalizedDirection := strings.ToLower(direction)
	if normalizedDirection != "import" && normalizedDirection != "export" {
		return "", fmt.Errorf("Provided direction '%s' does not map to either 'import' or 'export'", direction)
	}
	pieces := []string{"bgp_", "", "_", normalizedDirection, "FilterV", version}
	resizedName, err := truncateAndHashName(filterName, maxBIRDSymLen-len(strings.Join(pieces, "")))
	if err != nil {
		return "", err
	}
	pieces[1] = resizedName
	fullName := strings.Join(pieces, "")
	return fmt.Sprintf("'%s'", fullName), nil
}

// EmitBIRDExternalNetworkConfig generates BIRD config for the tables and kernel protocol configuration based on
// configured ExternalNetwork resources.
//
// e.g. for ExternalNetwork resource configured as follows:
//
// kind: ExternalNetwork
// apiVersion: projectcalico.org/v3
// metadata:
//
//	name: test-enet
//
// spec:
//
//	routeTableIndex: 7
//
// Would produce the following string array that can be easily output via BIRD config template:
//
//	[]string {
//	  "# ExternalNetwork test-enet",
//	  "table 'T_test-enet';",
//	  "protocol kernel 'K_test-enet' from kernel_template {",
//	  "  device routes yes;",
//	  "  table 'T_test-enet';",
//	  "  kernel table 7;",
//	  "  export filter {",
//	  "    print \"route: \", net, \", from, \", \", \", proto, \", \", bgp_next_hop;",
//	  "    if proto = \"Global_10_192_0_3\" then accept;",
//	  "    if proto = \"Global_10_192_0_4\" then accept;",
//	  "    reject;",
//	  "  };",
//	  "}",
//	 }
func EmitBIRDExternalNetworkConfig(selfIP string, externalNetworkKVPs memkv.KVPairs, globalPeersKVP memkv.KVPairs,
	nodeSpecificPeersKVP memkv.KVPairs) ([]string, error) {
	lines := []string{}
	peerReferencedExternalNetworks := map[string][]string{}
	var line string

	if len(externalNetworkKVPs) == 0 {
		return []string{"# No ExternalNetworks configured"}, nil
	}

	emitExternalNetworkProtoStatements := func(peerNamePrefix, selfIP string, peers memkv.KVPairs) (map[string][]string, error) {
		eNetProtoStatements := make(map[string][]string)
		for _, peer := range peers {
			var backendPeer backends.BGPPeer
			err := json.Unmarshal([]byte(peer.Value), &backendPeer)
			if err != nil {
				return map[string][]string{}, fmt.Errorf("Error unmarshalling JSON into backend BGPPeer: %s", err)
			}
			if backendPeer.PeerIP.String() == selfIP || backendPeer.ExternalNetwork == "" {
				continue // Skip ourselves because we don't generate a protocol definition for ourselves
			}
			var sep string
			if backendPeer.PeerIP.Version() == 4 {
				sep = "."
			} else {
				sep = ":"
			}
			ipParts := strings.Split(backendPeer.PeerIP.String(), sep)
			ipParts = append([]string{peerNamePrefix}, ipParts...)
			if backendPeer.Port > 0 {
				portStrParts := []string{"port", strconv.Itoa(int(backendPeer.Port))}
				ipParts = append(ipParts, portStrParts...)
			}
			eNetProtoStatements[backendPeer.ExternalNetwork] = append(eNetProtoStatements[backendPeer.ExternalNetwork],
				fmt.Sprintf("    if proto = \"%s\" then accept;", strings.Join(ipParts, "_")))
		}
		return eNetProtoStatements, nil
	}

	globalEnetProtoStatements, err := emitExternalNetworkProtoStatements("Global", selfIP, globalPeersKVP)
	if err != nil {
		return []string{}, err
	}

	explicitEnetProtoStatements, err := emitExternalNetworkProtoStatements("Node", selfIP, nodeSpecificPeersKVP)
	if err != nil {
		return []string{}, err
	}

	for enet, statements := range globalEnetProtoStatements {
		peerReferencedExternalNetworks[enet] = append(peerReferencedExternalNetworks[enet], statements...)
	}

	for enet, statements := range explicitEnetProtoStatements {
		peerReferencedExternalNetworks[enet] = append(peerReferencedExternalNetworks[enet], statements...)
	}

	if len(peerReferencedExternalNetworks) == 0 {
		line = "# No ExternalNetworks configured for any of this node's BGP peers"
		lines = append(lines, line)
		return lines, nil
	}
	for _, kvp := range externalNetworkKVPs {
		var externalNetwork v3.ExternalNetwork
		err := json.Unmarshal([]byte(kvp.Value), &externalNetwork)
		if err != nil {
			return []string{}, fmt.Errorf("Error unmarshalling JSON into ExternalNetwork: %s", err)
		}
		externalNetworkName := path.Base(kvp.Key)
		peerStatements, ok := peerReferencedExternalNetworks[externalNetworkName]
		if !ok {
			continue // No peers reference this external network so we don't need to emit any config for it
		}
		var routeTableIndex uint32
		if externalNetwork.Spec.RouteTableIndex != nil {
			routeTableIndex = *externalNetwork.Spec.RouteTableIndex
		}
		tableName, err := EmitExternalNetworkTableName(externalNetworkName)
		if err != nil {
			return []string{}, err
		}

		line = fmt.Sprintf("# ExternalNetwork %s", externalNetworkName)
		lines = append(lines, line)

		line = fmt.Sprintf("table %s;", tableName)
		lines = append(lines, line)

		kernelName := strings.Replace(tableName, "T_", "K_", 1)
		kernel := []string{
			fmt.Sprintf("protocol kernel %s from kernel_template {", kernelName),
			"  device routes yes;",
			fmt.Sprintf("  table %s;", tableName),
			fmt.Sprintf("  kernel table %d;", routeTableIndex),
			"  export filter {",
			"    print \"route: \", net, \", from, \", \", \", proto, \", \", bgp_next_hop;",
		}

		kernel = append(kernel, peerStatements...)

		kernel = append(kernel, []string{
			"    reject;",
			"  };",
			"}",
		}...)

		lines = append(lines, kernel...)

		directName := strings.Replace(tableName, "T_", "D_", 1)
		direct := []string{
			fmt.Sprintf("protocol direct %s from direct_template {", directName),
			fmt.Sprintf("  table %s;", tableName),
			"}",
		}
		lines = append(lines, direct...)

		staticName := strings.Replace(tableName, "T_", "S_", 1)
		static := []string{
			fmt.Sprintf("protocol static %s from static_template {", staticName),
			fmt.Sprintf("  table %s;", tableName),
			"}",
		}
		lines = append(lines, static...)
	}
	return lines, nil
}

// EmitBIRDBGPFilterFuncs generates a set of BIRD functions for BGPFilter resources that have been packaged into KVPairs.
// By doing the formatting inside of this function we eliminate the need to copy and paste repeated blocks of golang
// template code into our BIRD config templates that is both difficult to read and prone to errors
//
// e.g. for a BGPFilter resource specified as follows:
//
// kind: BGPFilter
// apiVersion: projectcalico.org/v3
// metadata:
//
//	name: test-bgpfilter
//
// spec:
//
//	exportV4:
//	  - action: Accept
//	    matchOperator: In
//	    cidr: 77.0.0.0/16
//	  - action: Reject
//	    matchOperator: In
//	    cidr: 77.1.0.0/16
//	importV4:
//	  - action: Accept
//	    matchOperator: In
//	    cidr: 44.0.0.0/16
//	  - action: Reject
//	    matchOperator: In
//	    cidr: 44.1.0.0/16
//
// Would produce the following string array that can be easily output via BIRD config template:
//
//	[]string{
//	  "# v4 BGPFilter test-bgpfilter",
//	  "function 'bgp_test-bgpfilter_importFilterV4'() {",
//	  "  if ((net ~ 44.0.0.0/16)) then { accept; }",
//	  "  if ((net ~ 44.1.0.0/16)) then { reject; }",
//	  "}",
//	  "function 'bgp_test-bgpfilter_exportFilterV4'() {",
//	  "  if ((net ~ 77.0.0.0/16)) then { accept; }",
//	  "  if ((net ~ 77.1.0.0/16)) then { reject; }",
//	  "}",
//	 }
func EmitBIRDBGPFilterFuncs(pairs memkv.KVPairs, version int) ([]string, error) {
	lines := []string{}
	var line string
	var versionStr string

	if version == 4 || version == 6 {
		versionStr = fmt.Sprintf("%d", version)
	} else {
		return []string{}, fmt.Errorf("Version must be either 4 or 6")
	}

	for _, kvp := range pairs {
		var filter v3.BGPFilter
		err := json.Unmarshal([]byte(kvp.Value), &filter)
		if err != nil {
			return []string{}, fmt.Errorf("Error unmarshalling JSON: %s", err)
		}

		importFiltersV4 := filter.Spec.ImportV4
		exportFiltersV4 := filter.Spec.ExportV4
		importFiltersV6 := filter.Spec.ImportV6
		exportFiltersV6 := filter.Spec.ExportV6

		var filterName string
		var emitImports bool
		var emitExports bool
		v4Selected := version == 4

		if v4Selected {
			emitImports = len(importFiltersV4) > 0
			emitExports = len(exportFiltersV4) > 0
		} else {
			emitImports = len(importFiltersV6) > 0
			emitExports = len(exportFiltersV6) > 0
		}

		if emitImports || emitExports {
			filterName = path.Base(kvp.Key)
			line = fmt.Sprintf("# v%s BGPFilter %s", versionStr, filterName)
			lines = append(lines, line)
		}

		var filterFuncName string
		var filterRule string
		if emitImports {
			filterFuncName, err = EmitBGPFilterFunctionName(filterName, "import", versionStr)
			if err != nil {
				return []string{}, err
			}
			line = fmt.Sprintf("function %s() {", filterFuncName)
			lines = append(lines, line)

			var ruleFields [][]string

			if v4Selected {
				for _, importV4 := range importFiltersV4 {
					ruleFields = append(ruleFields, []string{string(importV4.MatchOperator), importV4.CIDR,
						string(importV4.Source), string(importV4.Action)})
				}
			} else {
				for _, importV6 := range importFiltersV6 {
					ruleFields = append(ruleFields, []string{string(importV6.MatchOperator), importV6.CIDR,
						string(importV6.Source), string(importV6.Action)})
				}
			}

			for _, fields := range ruleFields {
				filterRule, err = emitBGPFilterStatement(fields[0], fields[1], fields[2], fields[3])
				if err != nil {
					return []string{}, err
				}
				line = fmt.Sprintf("  %s", filterRule)
				lines = append(lines, line)
			}

			line = "}"
			lines = append(lines, line)
		}

		if emitExports {
			filterFuncName, err = EmitBGPFilterFunctionName(filterName, "export", versionStr)
			if err != nil {
				return []string{}, err
			}
			line = fmt.Sprintf("function %s() {", filterFuncName)
			lines = append(lines, line)

			var ruleFields [][]string

			if v4Selected {
				for _, exportV4 := range exportFiltersV4 {
					ruleFields = append(ruleFields, []string{string(exportV4.MatchOperator), exportV4.CIDR,
						string(exportV4.Source), string(exportV4.Action)})
				}
			} else {
				for _, exportV6 := range exportFiltersV6 {
					ruleFields = append(ruleFields, []string{string(exportV6.MatchOperator), exportV6.CIDR,
						string(exportV6.Source), string(exportV6.Action)})
				}
			}

			for _, fields := range ruleFields {
				filterRule, err = emitBGPFilterStatement(fields[0], fields[1], fields[2], fields[3])
				if err != nil {
					return []string{}, err
				}
				line = fmt.Sprintf("  %s", filterRule)
				lines = append(lines, line)
			}

			line = "}"
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		line = fmt.Sprintf("# No v%s BGPFilters configured", versionStr)
		lines = append(lines, line)
	}
	return lines, nil
}

// The maximum length of a k8s resource (253 bytes) is longer than the maximum length of BIRD symbols (64 chars).
// This function provides a way to map the k8s resource name to a BIRD symbol name that accounts
// for the length difference in a way that minimizes the chance of collisions
func truncateAndHashName(name string, maxLen int) (string, error) {
	if len(name) <= maxLen {
		return name, nil
	}
	// SHA256 outputs a hash 64 chars long but we'll use only the first 16
	hashCharsToUse := 16
	// Account for underscore we insert between truncated name and hash string
	hashStrSize := hashCharsToUse + 1
	if maxLen <= hashStrSize {
		return "", fmt.Errorf("Max truncated string length must be greater than the mininum size of %d",
			hashStrSize)
	}
	hash := sha256.New()
	_, err := hash.Write([]byte(name))
	if err != nil {
		return "", err
	}
	truncationLen := maxLen - hashStrSize
	hashStr := fmt.Sprintf("%X", hash.Sum(nil))
	truncatedName := fmt.Sprintf("%s_%s", name[:truncationLen], hashStr[:hashCharsToUse])
	return truncatedName, nil
}

// hashToIPv4 hashes the given string and
// formats the resulting 4 bytes as an IPv4 address.
func hashToIPv4(nodeName string) string {
	hash := sha256.New()
	_, err := hash.Write([]byte(nodeName))
	if err != nil {
		return ""
	}
	hashBytes := hash.Sum(nil)
	ip := hashBytes[:4]
	//BGP doesn't allow router IDs in special IP ranges (e.g., 224.x.x.x)
	ip0Value := int(ip[0])
	if ip0Value > 223 {
		ip0Value = ip0Value - 32
	}
	routerId := strconv.Itoa(ip0Value) + "." +
		strconv.Itoa(int(ip[1])) + "." +
		strconv.Itoa(int(ip[2])) + "." +
		strconv.Itoa(int(ip[3]))
	return routerId
}

// Getenv retrieves the value of the environment variable named by the key.
// It returns the value, which will the default value if the variable is not present.
// If no default value was given - returns "".
func Getenv(key string, v ...string) string {
	defaultValue := ""
	if len(v) > 0 {
		defaultValue = v[0]
	}

	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// CreateMap creates a key-value map of string -> interface{}
// The i'th is the key and the i+1 is the value
func CreateMap(values ...interface{}) (map[string]interface{}, error) {
	if len(values)%2 != 0 {
		return nil, errors.New("invalid map call")
	}
	dict := make(map[string]interface{}, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, errors.New("map keys must be strings")
		}
		dict[key] = values[i+1]
	}
	return dict, nil
}

func UnmarshalJsonObject(data string) (map[string]interface{}, error) {
	var ret map[string]interface{}
	err := json.Unmarshal([]byte(data), &ret)
	return ret, err
}

func UnmarshalJsonArray(data string) ([]interface{}, error) {
	var ret []interface{}
	err := json.Unmarshal([]byte(data), &ret)
	return ret, err
}

func LookupIP(data string) []string {
	ips, err := net.LookupIP(data)
	if err != nil {
		return nil
	}
	// "Cast" IPs into strings and sort the array
	ipStrings := make([]string, len(ips))

	for i, ip := range ips {
		ipStrings[i] = ip.String()
	}
	sort.Strings(ipStrings)
	return ipStrings
}

type sortSRV []*net.SRV

func (s sortSRV) Len() int {
	return len(s)
}

func (s sortSRV) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s sortSRV) Less(i, j int) bool {
	str1 := fmt.Sprintf("%s%d%d%d", s[i].Target, s[i].Port, s[i].Priority, s[i].Weight)
	str2 := fmt.Sprintf("%s%d%d%d", s[j].Target, s[j].Port, s[j].Priority, s[j].Weight)
	return str1 < str2
}

func LookupSRV(service, proto, name string) []*net.SRV {
	_, addrs, err := net.LookupSRV(service, proto, name)
	if err != nil {
		return []*net.SRV{}
	}
	sort.Sort(sortSRV(addrs))
	return addrs
}

func Base64Encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func Base64Decode(data string) (string, error) {
	s, err := base64.StdEncoding.DecodeString(data)
	return string(s), err
}

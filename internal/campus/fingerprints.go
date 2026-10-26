package campus

import (
	"net"
	"strings"
)

// NetworkFingerprint defines the network observation criteria for identifying a campus.
type NetworkFingerprint struct {
	Campus     Campus
	SubnetCIDR string
	GatewayIP  string
	GatewayMAC string
	Comment    string
}

// KnownFingerprints is the central table of observed campus gateway and subnet fingerprints.
// To support new campuses, subnets or gateway hardware replacements, append entries here.
var KnownFingerprints = []NetworkFingerprint{
	{
		Campus:     Wenchang,
		SubnetCIDR: "10.52.128.0/18",
		GatewayIP:  "10.52.191.254",
		GatewayMAC: "7C-1E-06-8F-98-02",
		Comment:    "文昌校区有线与无线观测样本",
	},
	{
		Campus:     Nanhu,
		SubnetCIDR: "10.3.0.0/16",
		GatewayIP:  "10.3.255.254",
		GatewayMAC: "80-05-88-46-B4-F9",
		Comment:    "南湖校区无线 CUMT_Stu 观测样本",
	},
}

type compiledFingerprint struct {
	campus     Campus
	subnet     *net.IPNet
	gatewayIP  string
	gatewayMAC string
}

var compiledTable = func() []compiledFingerprint {
	list := make([]compiledFingerprint, 0, len(KnownFingerprints))
	for _, fp := range KnownFingerprints {
		_, ipNet, err := net.ParseCIDR(fp.SubnetCIDR)
		if err != nil {
			continue
		}
		list = append(list, compiledFingerprint{
			campus:     fp.Campus,
			subnet:     ipNet,
			gatewayIP:  fp.GatewayIP,
			gatewayMAC: strings.ToUpper(strings.ReplaceAll(fp.GatewayMAC, ":", "-")),
		})
	}
	return list
}()

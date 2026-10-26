package portal

const (
	// DefaultBackendURL is the fixed eportal login submission endpoint for both campuses.
	DefaultBackendURL = "http://10.2.5.251:801/eportal/"

	// PrimaryProbeURL is the Microsoft NCSI connect test URL used as the primary probe target.
	PrimaryProbeURL = "http://www.msftconnecttest.com/connecttest.txt"

	// PrimaryProbeExpectedContent is the string content expected from PrimaryProbeURL when online.
	PrimaryProbeExpectedContent = "Microsoft Connect Test"

	// SecondaryProbeURL is the domestic HTTPS fallback target to verify general outbound connectivity.
	SecondaryProbeURL = "https://www.baidu.com/"
)

// ProbeURL preserves backward compatibility with tests and callers referencing portal.ProbeURL.
const ProbeURL = PrimaryProbeURL

// RedirectDiscoveryTargets are plain-HTTP endpoints queried to capture campus 302/meta redirects.
var RedirectDiscoveryTargets = []string{
	"http://9.9.9.9/",
	"http://neverssl.com/",
}

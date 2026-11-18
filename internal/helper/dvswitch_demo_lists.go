package helper

import (
	"context"
	"os"
	"path/filepath"
)

// Placeholder host lists for the demo and tests: made-up names and RFC 5737
// addresses. The demo writes them below its own temporary directory, so it
// never reads the real /var/lib/mmdvm.
var demoDVLists = map[string]string{
	"DMR_Hosts.txt": "# DMR_Hosts.txt (placeholder)\n" +
		"DMRGateway\t\t\t0000\t127.0.0.1\t\t\t\tnone\t\t62031\n" +
		"BM_0000_Example\t\t\t0000\texample-master.invalid\t\texample-pass\t62031\n" +
		"BM_2001_Example\t\t\t2001\t192.0.2.11\t\t\texample-pass\t62031\n" +
		"BM_3100_Example\t\t\t3100\t192.0.2.12\t\t\texample-pass\t62031\n" +
		"XLX_000\t\t\t\t0000\t198.51.100.20\t\t\texample-pass\t62030\n" +
		"TGIF_Example\t\t\t0000\t203.0.113.30\t\t\texample-pass\t62031\n",
	"YSFHosts.txt": "# YSFHosts.txt (placeholder)\n" +
		"00001;US-Example;Example reflector;192.0.2.21;42000;000;\n" +
		"00002;XX-Europe-Example;Example link;198.51.100.22;42000;000;\n" +
		"00003;XX-World-Example;Example world link;203.0.113.23;42500;000;\n",
	"P25Hosts.txt": "# P25Hosts.txt (placeholder)\n" +
		"10100\t192.0.2.31\t41000\n" +
		"10200\t198.51.100.32\t41000\n" +
		"31100\t203.0.113.33\t41000\n",
	"NXDNHosts.txt": "# NXDNHosts.txt (placeholder)\n" +
		"10\t192.0.2.41\t41400\n" +
		"20\t198.51.100.42\t41400\n" +
		"65\t203.0.113.43\t41400\n",
}

// writeDemoDVLists puts the placeholder lists into dir.
func writeDemoDVLists(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for n, c := range demoDVLists {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// DVSwitchLists implements the API's DVSwitch interface. Lists is an Ops
// whose host dir is a temporary one (NewDemoOps); without it there is no list.
func (d *DemoDVSwitch) DVSwitchLists(ctx context.Context, mode string) (DVList, error) {
	if d.Lists == nil {
		if !ValidDVListMode(mode) {
			return DVList{}, dvInvalid("mode")
		}
		return DVList{}, ErrDVNoList
	}
	return d.Lists.DVSwitchLists(ctx, &DVSwitchRequest{Mode: mode})
}

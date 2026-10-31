package hidpad

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad/hidreport"
)

const (
	dualsenseName    = "DualSense Wireless Controller"
	dualsenseVendor  = 0x054C
	dualsenseProduct = 0x0CE6
	dualsenseVersion = 0x0100

	hostGetFeature      uint32 = 0x01
	hostSetFeature      uint32 = 0x02
	hostWriteReport     uint32 = 0x04
	supportedHostEvents        = hostGetFeature | hostSetFeature | hostWriteReport
)

// DualSenseWired is the wired USB DualSense profile.
var DualSenseWired = Profile{
	name:      dualsenseName,
	vendorID:  dualsenseVendor,
	productID: dualsenseProduct,
	version:   dualsenseVersion,
}

var identities = struct {
	sync.Mutex
	ids  map[string]struct{}
	macs map[string]string
}{ids: make(map[string]struct{}), macs: make(map[string]string)}

func reserveID(requested string) (string, error) {
	if requested != "" {
		if !validDeviceID(requested) {
			return "", fmt.Errorf("%w: device ID must match pad2go-<lowercase hex>", ErrBadFeature)
		}
		identities.Lock()
		defer identities.Unlock()
		if _, exists := identities.ids[requested]; exists {
			return "", fmt.Errorf("%w: duplicate device ID %q", ErrBadFeature, requested)
		}
		identities.ids[requested] = struct{}{}
		return requested, nil
	}

	for {
		randomID := make([]byte, 12)
		if _, err := rand.Read(randomID); err != nil {
			return "", fmt.Errorf("generate device identity: %w", err)
		}
		id := "pad2go-" + hex.EncodeToString(randomID)
		identities.Lock()
		if _, exists := identities.ids[id]; !exists {
			identities.ids[id] = struct{}{}
			identities.Unlock()
			return id, nil
		}
		identities.Unlock()
	}
}

func validDeviceID(id string) bool {
	const prefix = "pad2go-"
	if len(id) <= len(prefix) || id[:len(prefix)] != prefix {
		return false
	}
	for _, digit := range id[len(prefix):] {
		if digit < '0' || digit > '9' {
			if digit < 'a' || digit > 'f' {
				return false
			}
		}
	}
	return true
}

func reserveMAC(id string, mac []byte) error {
	key := string(mac)
	identities.Lock()
	defer identities.Unlock()
	if owner, exists := identities.macs[key]; exists && owner != id {
		return fmt.Errorf("%w: MAC is already assigned", ErrBadFeature)
	}
	identities.macs[key] = id
	return nil
}

func releaseIdentity(id string, mac []byte) {
	identities.Lock()
	delete(identities.ids, id)
	if len(mac) != 0 {
		key := string(mac)
		if identities.macs[key] == id {
			delete(identities.macs, key)
		}
	}
	identities.Unlock()
}

func identityMAC(id string) []byte {
	sum := sha256.Sum256([]byte(id))
	mac := append([]byte(nil), sum[:6]...)
	mac[0] = 0x02               // locally administered, unicast
	mac[1], mac[2] = 0x50, 0x32 // "P2": the pad2go virtual-device prefix
	return mac
}

func neutralReport() []byte {
	report := make([]byte, inputReportLen)
	report[0] = reportInputID
	for i := 1; i <= 4; i++ {
		report[i] = 0x80
	}
	report[8] = 0x08
	report[33] = 0x80
	report[37] = 0x80
	report[53] = 0x0A
	return report
}

func buildDescriptor() []byte { return hidreport.BuildDualSense() }

func supportedPlatform(goos, arch string) bool {
	switch goos {
	case "windows":
		return arch == "amd64" || arch == "arm64"
	case "linux":
		return true
	default:
		return false
	}
}

func validLocalUnicastMAC(mac []byte) bool {
	return len(mac) == 6 && mac[0]&0x02 != 0 && mac[0]&0x01 == 0
}

// dualsenseUniq formats the feature 0x09 MAC the way hid-playstation does.
// The driver stores the bytes in little-endian order and prints hdev->uniq
// with %pMR, which reverses them into the conventional colon form.
func dualsenseUniq(mac []byte) string {
	if len(mac) != 6 {
		return ""
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[5], mac[4], mac[3], mac[2], mac[1], mac[0])
}

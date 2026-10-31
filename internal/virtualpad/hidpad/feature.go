package hidpad

import (
	"encoding/binary"
	"fmt"
)

var featureDataLengths = map[byte]int{
	0x05: 40, 0x08: 47, 0x09: 19, 0x0A: 26, 0x20: 63,
	0x21: 4, 0x22: 63, 0x80: 63, 0x81: 63, 0x82: 9,
	0x83: 63, 0x84: 63, 0x85: 2, 0xA0: 1, 0xE0: 63,
	0xF0: 63, 0xF1: 63, 0xF2: 15, 0xF4: 63, 0xF5: 3,
}

func featureLength(reportID byte) (int, bool) {
	length, ok := featureDataLengths[reportID]
	return length, ok
}

func featureLengthTotal(reportID byte) int {
	length, _ := featureLength(reportID)
	return length + 1
}

func makeFeatures(profile Profile, id string, overrides map[byte][]byte) (map[byte][]byte, []byte, error) {
	mac := identityMAC(id)
	features := make(map[byte][]byte, len(featureDataLengths))
	for reportID, dataLength := range featureDataLengths {
		features[reportID] = make([]byte, dataLength+1)
		features[reportID][0] = reportID
	}

	calibration := features[0x05]
	for offset, value := range map[int]int16{
		1: 0, 3: 0, 5: 0,
		7: 8192, 9: -8192, 11: 8192, 13: -8192, 15: 8192, 17: -8192,
		19: 16384, 21: 16384,
		23: 8192, 25: -8192, 27: 8192, 29: -8192, 31: 8192, 33: -8192,
	} {
		binary.LittleEndian.PutUint16(calibration[offset:offset+2], uint16(value))
	}

	copy(features[0x09][1:7], mac)
	firmware := features[0x20]
	binary.LittleEndian.PutUint32(firmware[24:28], 3)
	binary.LittleEndian.PutUint32(firmware[28:32], 0x00000015)
	binary.LittleEndian.PutUint16(firmware[44:46], 0x0221)

	if profile != DualSenseWired {
		return nil, nil, fmt.Errorf("%w: unsupported profile %q", ErrUnsupported, profile.name)
	}
	for reportID, replacement := range overrides {
		length, declared := featureLength(reportID)
		if !declared {
			return nil, nil, fmt.Errorf("%w: 0x%02x", ErrUnsupportedFeature, reportID)
		}
		if len(replacement) != length+1 || replacement[0] != reportID {
			return nil, nil, fmt.Errorf("%w: report 0x%02x must be %d bytes and start with its ID", ErrBadFeature, reportID, length+1)
		}
		if reportID == 0x09 && !validLocalUnicastMAC(replacement[1:7]) {
			return nil, nil, fmt.Errorf("%w: feature 0x09 MAC must be locally administered unicast", ErrBadFeature)
		}
		features[reportID] = append([]byte(nil), replacement...)
	}
	if override, ok := overrides[0x09]; ok {
		mac = append([]byte(nil), override[1:7]...)
	}
	return features, mac, nil
}

func featureReply(reportID byte, reports map[byte][]byte) ([]byte, error) {
	if _, ok := featureLength(reportID); !ok {
		return nil, fmt.Errorf("%w: 0x%02x", ErrUnsupportedFeature, reportID)
	}
	report, ok := reports[reportID]
	if !ok {
		return nil, fmt.Errorf("%w: no profile data for 0x%02x", ErrBadFeature, reportID)
	}
	return append([]byte(nil), report...), nil
}

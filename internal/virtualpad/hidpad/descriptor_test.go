package hidpad

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad/hidreport"
)

func TestDescriptorReportTable(t *testing.T) {
	descriptor := hidreport.BuildDualSense()
	if len(descriptor) != 273 {
		t.Fatalf("descriptor length = %d, want 273", len(descriptor))
	}
	parsed, err := parseHIDDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	want := map[reportKey]int{
		{kind: "input", id: 0x01}:  63,
		{kind: "output", id: 0x02}: 47,
	}
	for id, length := range featureDataLengths {
		want[reportKey{kind: "feature", id: id}] = length
	}
	if len(parsed.reportBits) != len(want) {
		t.Fatalf("report count = %d, want %d", len(parsed.reportBits), len(want))
	}
	for key, bytes := range want {
		bits, ok := parsed.reportBits[key]
		if !ok {
			t.Errorf("missing %s report %#02x", key.kind, key.id)
			continue
		}
		if bits%8 != 0 || bits/8 != bytes {
			t.Errorf("%s report %#02x has %d bits, want %d data bytes", key.kind, key.id, bits, bytes)
		}
	}
	wantUsages := map[reportKey]uint32{
		{kind: "output", id: 0x02}:  0x23,
		{kind: "feature", id: 0x05}: 0x33,
		{kind: "feature", id: 0x08}: 0x34,
		{kind: "feature", id: 0x09}: 0x24,
		{kind: "feature", id: 0x0A}: 0x25,
		{kind: "feature", id: 0x20}: 0x26,
		{kind: "feature", id: 0x21}: 0x27,
		{kind: "feature", id: 0x22}: 0x40,
		{kind: "feature", id: 0x80}: 0x28,
		{kind: "feature", id: 0x81}: 0x29,
		{kind: "feature", id: 0x82}: 0x2A,
		{kind: "feature", id: 0x83}: 0x2B,
		{kind: "feature", id: 0x84}: 0x2C,
		{kind: "feature", id: 0x85}: 0x2D,
		{kind: "feature", id: 0xA0}: 0x2E,
		{kind: "feature", id: 0xE0}: 0x2F,
		{kind: "feature", id: 0xF0}: 0x30,
		{kind: "feature", id: 0xF1}: 0x31,
		{kind: "feature", id: 0xF2}: 0x32,
		{kind: "feature", id: 0xF4}: 0x35,
		{kind: "feature", id: 0xF5}: 0x36,
	}
	for key, usage := range wantUsages {
		var found bool
		for _, field := range parsed.fields {
			if field.key != key {
				continue
			}
			found = field.usagePage == 0xFF00 && len(field.usages) == 1 && field.usages[0] == usage
			break
		}
		if !found {
			t.Errorf("%s report %#02x usage does not match page 0xFF00 / usage %#02x", key.kind, key.id, usage)
		}
	}
}

func TestDescriptorDeterministic(t *testing.T) {
	first, second := hidreport.BuildDualSense(), hidreport.BuildDualSense()
	if !bytes.Equal(first, second) {
		t.Fatal("descriptor builder is not deterministic")
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return this test path")
	}
	root := filepath.Dir(currentFile)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(source, []byte("//go:"+"embed")) {
			return fmt.Errorf("embedded asset directive found in %s", path)
		}
		if match := numericByteLiteral.Find(source); match != nil {
			return fmt.Errorf("numeric byte blob found in %s: %.80s", path, match)
		}
		if match := escapedByteLiteral.Find(source); match != nil {
			return fmt.Errorf("escaped byte blob found in %s: %.80s", path, match)
		}
		if match := longHexLiteral.Find(source); match != nil {
			return fmt.Errorf("hex-string blob found in %s: %.80s", path, match)
		}
		if len(hexByteToken.FindAll(source, -1)) > 200 {
			return fmt.Errorf("large hex byte blob found in %s", path)
		}
		if bytes.Contains(source, []byte("hex."+"DecodeString")) ||
			bytes.Contains(source, []byte("base64."+"StdEncoding")) ||
			bytes.Contains(source, []byte("base64."+"RawStdEncoding")) {
			return fmt.Errorf("encoded blob decoder found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// These patterns catch a descriptor pasted as a literal instead of composed
// from HID items. Twelve consecutive values is well above any legitimate
// usage list in the builder.
var (
	numericByteLiteral = regexp.MustCompile(`(?:\b(?:0[xX][0-9A-Fa-f]{1,2}|[0-9]{1,3})\s*,\s*){12,}`)
	escapedByteLiteral = regexp.MustCompile(`(?:\\x[0-9A-Fa-f]{2}){12,}`)
	longHexLiteral     = regexp.MustCompile(`"[0-9A-Fa-f]{64,}"`)
	hexByteToken       = regexp.MustCompile(`\b0[xX][0-9A-Fa-f]{2}\b`)
)

func TestDescriptorFieldSummary(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return this test path")
	}
	factPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "docs", "dualsense-usb-facts.md")
	facts, err := os.ReadFile(factPath)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := parseFactFields(string(facts))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseHIDDescriptor(hidreport.BuildDualSense())
	if err != nil {
		t.Fatal(err)
	}
	actual := parsed.inputSummary()
	if len(actual) != len(expected) {
		t.Fatalf("parsed input summary has %d rows, fact sheet has %d", len(actual), len(expected))
	}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Errorf("field %d = %+v, want fact-sheet row %+v", i, actual[i], expected[i])
		}
	}
}

type reportKey struct {
	kind string
	id   byte
}

type descriptorField struct {
	key       reportKey
	usagePage uint32
	usages    []uint32
	offset    int
	size      int
	count     int
}

type descriptorParser struct {
	reportID    byte
	usagePage   uint32
	reportSize  int
	reportCount int
	logicalMin  int32
	logicalMax  int32
	physicalMin int32
	physicalMax int32
	unit        uint32
	usages      []uint32
	usageMin    *uint32
	usageMax    *uint32
	reportBits  map[reportKey]int
	fields      []descriptorField
}

func parseHIDDescriptor(data []byte) (*descriptorParser, error) {
	parser := &descriptorParser{reportBits: make(map[reportKey]int)}
	var collectionDepth int
	for offset := 0; offset < len(data); {
		prefix := data[offset]
		offset++
		if prefix == 0xFE {
			if offset+2 > len(data) {
				return nil, fmt.Errorf("truncated long item at %d", offset-1)
			}
			length := int(data[offset])
			offset += 2
			if offset+length > len(data) {
				return nil, fmt.Errorf("truncated long item data at %d", offset)
			}
			offset += length
			continue
		}
		sizeCode := int(prefix & 0x03)
		size := sizeCode
		if sizeCode == 3 {
			size = 4
		}
		if offset+size > len(data) {
			return nil, fmt.Errorf("truncated short item at %d", offset-1)
		}
		var value uint32
		for i := 0; i < size; i++ {
			value |= uint32(data[offset+i]) << (8 * i)
		}
		offset += size
		itemType := (prefix >> 2) & 0x03
		tag := prefix >> 4
		switch itemType {
		case 1:
			parser.parseGlobal(tag, value, size)
		case 2:
			parser.parseLocal(tag, value)
		case 0:
			switch tag {
			case 8, 9, 11:
				kind := map[byte]string{8: "input", 9: "output", 11: "feature"}[tag]
				key := reportKey{kind: kind, id: parser.reportID}
				bitOffset := parser.reportBits[key]
				bitSize := parser.reportSize * parser.reportCount
				usages := parser.consumeUsages()
				parser.fields = append(parser.fields, descriptorField{
					key: key, usagePage: parser.usagePage, usages: usages,
					offset: bitOffset, size: bitSize, count: parser.reportCount,
				})
				parser.reportBits[key] += bitSize
			case 10:
				collectionDepth++
				parser.clearLocal()
			case 12:
				collectionDepth--
				if collectionDepth < 0 {
					return nil, fmt.Errorf("collection closed before it opened")
				}
				parser.clearLocal()
			}
		}
	}
	if collectionDepth != 0 {
		return nil, fmt.Errorf("descriptor ended with collection depth %d", collectionDepth)
	}
	return parser, nil
}

func (p *descriptorParser) parseGlobal(tag byte, value uint32, size int) {
	switch tag {
	case 0:
		p.usagePage = value
	case 1:
		p.logicalMin = signExtend(value, size)
	case 2:
		if p.logicalMin < 0 {
			p.logicalMax = signExtend(value, size)
		} else {
			p.logicalMax = int32(value)
		}
	case 3:
		p.physicalMin = signExtend(value, size)
	case 4:
		if p.physicalMin < 0 {
			p.physicalMax = signExtend(value, size)
		} else {
			p.physicalMax = int32(value)
		}
	case 6:
		p.unit = value
	case 7:
		p.reportSize = int(value)
	case 8:
		p.reportID = byte(value)
	case 9:
		p.reportCount = int(value)
	}
}

func (p *descriptorParser) parseLocal(tag byte, value uint32) {
	switch tag {
	case 0:
		p.usages = append(p.usages, value)
	case 1:
		v := value
		p.usageMin = &v
	case 2:
		v := value
		p.usageMax = &v
	}
}

func (p *descriptorParser) consumeUsages() []uint32 {
	usages := append([]uint32(nil), p.usages...)
	if p.usageMin != nil && p.usageMax != nil && *p.usageMax >= *p.usageMin {
		for value := *p.usageMin; value <= *p.usageMax; value++ {
			usages = append(usages, value)
		}
	}
	p.clearLocal()
	return usages
}

func (p *descriptorParser) clearLocal() {
	p.usages = nil
	p.usageMin = nil
	p.usageMax = nil
}

func signExtend(value uint32, size int) int32 {
	if size == 0 || size == 4 {
		return int32(value)
	}
	bits := uint(size * 8)
	sign := uint32(1) << (bits - 1)
	if value&sign != 0 {
		value |= ^uint32(0) << bits
	}
	return int32(value)
}

type summaryField struct {
	page   uint32
	usage  string
	offset int
	size   int
}

func (p *descriptorParser) inputSummary() []summaryField {
	var summary []summaryField
	for _, field := range p.fields {
		if field.key.kind != "input" || field.key.id != 0x01 || field.count == 0 {
			continue
		}
		if field.usagePage == 0x09 && len(field.usages) > 1 {
			summary = append(summary, summaryField{
				page:   field.usagePage,
				usage:  fmt.Sprintf("%d..%d", field.usages[0], field.usages[len(field.usages)-1]),
				offset: field.offset,
				size:   field.size,
			})
			continue
		}
		if len(field.usages) == field.count && field.count > 1 {
			for i, usage := range field.usages {
				summary = append(summary, summaryField{
					page: field.usagePage, usage: strconv.FormatUint(uint64(usage), 10),
					offset: field.offset + i*field.size/field.count, size: field.size / field.count,
				})
			}
			continue
		}
		usage := uint32(0)
		if len(field.usages) != 0 {
			usage = field.usages[0]
		}
		summary = append(summary, summaryField{
			page: field.usagePage, usage: strconv.FormatUint(uint64(usage), 10),
			offset: field.offset, size: field.size,
		})
	}
	return summary
}

var factRowPattern = regexp.MustCompile(`^\| 0x01 \| ([0-9]+) \| ([0-9]+) \| (0x[0-9A-Fa-f]+) \| (.*?) \|`)

func parseFactFields(facts string) ([]summaryField, error) {
	var fields []summaryField
	inTable := false
	for _, line := range strings.Split(facts, "\n") {
		if strings.HasPrefix(line, "| Report | Bit offset |") {
			inTable = true
			continue
		}
		if !inTable || !strings.HasPrefix(line, "|") {
			continue
		}
		match := factRowPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		offset, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(match[2])
		if err != nil {
			return nil, err
		}
		page, err := strconv.ParseUint(match[3], 0, 32)
		if err != nil {
			return nil, err
		}
		usageText := match[4]
		usages := regexp.MustCompile(`0x[0-9A-Fa-f]+`).FindAllString(usageText, -1)
		usage := ""
		if len(usages) != 0 {
			last := usages[len(usages)-1]
			value, parseErr := strconv.ParseUint(last, 0, 32)
			if parseErr != nil {
				return nil, parseErr
			}
			usage = strconv.FormatUint(value, 10)
		} else {
			rangeMatch := regexp.MustCompile(`([0-9]+)\.\.([0-9]+)`).FindStringSubmatch(usageText)
			if rangeMatch != nil {
				usage = rangeMatch[1] + ".." + rangeMatch[2]
			} else {
				return nil, fmt.Errorf("cannot parse usage from fact row %q", usageText)
			}
		}
		fields = append(fields, summaryField{page: uint32(page), usage: usage, offset: offset, size: size})
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("fact sheet input field summary was not found")
	}
	return fields, nil
}

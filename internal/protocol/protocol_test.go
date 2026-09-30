package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Golden values below were produced by the original Python implementation.

func TestVibrationBytesMatchesOriginal(t *testing.T) {
	cases := []struct {
		v    Vibration
		want string
	}{
		{SilentVibration(), "e100101e00"},
		{Vibration{LFFreq: 0x060, LFAmp: 0x350, HFFreq: 0x0c0, HFAmp: 0x250}, "60400d0c94"},
		{Vibration{LFFreq: 0x1ff, LFEnTone: true, LFAmp: 1023, HFFreq: 0x1ff, HFEnTone: true, HFAmp: 1023}, "ffffffffff"},
	}
	for _, c := range cases {
		got := c.v.Bytes()
		if hex.EncodeToString(got[:]) != c.want {
			t.Errorf("%+v: got %x, want %s", c.v, got, c.want)
		}
	}
}

func TestEncodeMemoryReadCommandMatchesOriginal(t *testing.T) {
	arg, err := MemoryReadData(0x40, AddrControllerInfo)
	if err != nil {
		t.Fatal(err)
	}
	got := EncodeCommand(CmdMemory, SubMemoryRead, arg)
	if want := mustHex(t, "0291010400080000407e000000300100"); !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if _, err := MemoryReadData(0x50, 0); err == nil {
		t.Fatal("expected error for oversize read")
	}
}

func TestPairSetMACMatchesOriginal(t *testing.T) {
	got := PairSetMACData(0xAABBCCDDEEFF)
	if want := mustHex(t, "0002ffeeddccbbaaffeeddccbbaa"); !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestLimitJoyConAmplitudeMatchesOriginal(t *testing.T) {
	v := Vibration{LFAmp: 900, HFAmp: 600}.LimitJoyConAmplitude()
	if v.LFAmp != 614 || v.HFAmp != 409 {
		t.Fatalf("got %d/%d, want 614/409", v.LFAmp, v.HFAmp)
	}
	v = Vibration{LFAmp: 100, HFAmp: 200}.LimitJoyConAmplitude()
	if v.LFAmp != 100 || v.HFAmp != 200 {
		t.Fatalf("under budget should be unchanged, got %d/%d", v.LFAmp, v.HFAmp)
	}
}

func TestDecodeResponse(t *testing.T) {
	resp := []byte{0x09, 0x01, 0, 0, 0, 0, 0, 0, 0xAA}
	payload, err := DecodeResponse(0x09, resp)
	if err != nil || !bytes.Equal(payload, []byte{0xAA}) {
		t.Fatalf("payload %x err %v", payload, err)
	}
	if _, err := DecodeResponse(0x09, []byte{0x09, 0x04, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("status != 1 should fail")
	}
	if _, err := DecodeResponse(0x0A, resp); err == nil {
		t.Fatal("wrong command id should fail")
	}
}

func TestInitSequenceSkips0101ForPro2(t *testing.T) {
	for _, c := range InitSequenceFor(ProController2PID) {
		if c.Cmd == 0x01 && c.Sub == 0x01 {
			t.Fatal("Pro Controller 2 sequence must not contain 01:01")
		}
	}
	if len(InitSequenceFor(JoyCon2LeftPID)) != len(SW2InitSequence) {
		t.Fatal("Joy-Con sequence should be the full sequence")
	}
}

func TestLEDData(t *testing.T) {
	if got := LEDData(3, false); !bytes.Equal(got, []byte{0x07, 0, 0, 0}) {
		t.Fatalf("player 3: %x", got)
	}
	if got := LEDData(1, true); got[0] != 0x08 {
		t.Fatalf("reversed player 1: %x", got)
	}
	if got := LEDData(12, false); got[0] != LEDPattern[8] {
		t.Fatalf("clamped player: %x", got)
	}
}

func TestStickXY(t *testing.T) {
	// x=0x123, y=0xABC packed little-endian into 24 bits.
	v := 0x123 | 0xABC<<12
	x, y := StickXY([]byte{byte(v), byte(v >> 8), byte(v >> 16)})
	if x != 0x123 || y != 0xABC {
		t.Fatalf("got %x,%x", x, y)
	}
}

func packStick(x, y int) []byte {
	v := x | y<<12
	return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
}

func TestParseAdvertisement(t *testing.T) {
	data := make([]byte, 16)
	binary.LittleEndian.PutUint16(data[3:], NintendoVendorID)
	binary.LittleEndian.PutUint16(data[5:], JoyCon2RightPID)
	adv, err := ParseAdvertisement(data)
	if err != nil || !adv.Supported() || !adv.Pairing() {
		t.Fatalf("adv %+v err %v", adv, err)
	}
	copy(data[10:], []byte{0xFF, 0xEE, 0xDD, 0xCC, 0xBB, 0xAA})
	adv, _ = ParseAdvertisement(data)
	if adv.Pairing() || adv.ReconnectMAC != 0xAABBCCDDEEFF {
		t.Fatalf("reconnect MAC %x", adv.ReconnectMAC)
	}
	if _, err := ParseAdvertisement(data[:10]); err == nil {
		t.Fatal("short data should fail")
	}
}

func TestParseControllerInfo(t *testing.T) {
	b := make([]byte, 0x40)
	copy(b[2:], "XJW10012345678")
	binary.LittleEndian.PutUint16(b[18:], NintendoVendorID)
	binary.LittleEndian.PutUint16(b[20:], ProController2PID)
	copy(b[25:], []byte{1, 2, 3})
	info, err := ParseControllerInfo(b)
	if err != nil {
		t.Fatal(err)
	}
	if info.SerialNumber != "XJW10012345678" || info.ProductID != ProController2PID || info.Colors[0] != [3]byte{1, 2, 3} {
		t.Fatalf("%+v", info)
	}
}

func TestStickCalibration(t *testing.T) {
	raw := append(append(packStick(2000, 2100), packStick(1400, 1300)...), packStick(1500, 1600)...)
	c := ParseStickCalibration(raw)
	if !c.Valid || c.CenterX != 2000 || c.MinX != 1400 || c.MaxY != 1600 {
		t.Fatalf("%+v", c)
	}
	x, y := c.Apply(2000+1500, 2100-1300, 1, 0.03)
	if x != 1 || y != -1 {
		t.Fatalf("full deflection: %v,%v", x, y)
	}
	x, y = c.Apply(2010, 2105, 1, 0.03)
	if x != 0 || y != 0 {
		t.Fatalf("deadzone: %v,%v", x, y)
	}
	x, _ = c.Apply(2000+750, 2100, 1, 0.03)
	if math.Abs(x-0.5) > 1e-9 {
		t.Fatalf("half deflection: %v", x)
	}

	if bad := ParseStickCalibration(make([]byte, 9)); bad.Valid || bad != DefaultStickCalibration() {
		t.Fatalf("zeros should fall back to defaults: %+v", bad)
	}
	if bad := ParseStickCalibration(append(append(packStick(500, 2048), packStick(400, 400)...), packStick(400, 400)...)); bad.Valid {
		t.Fatal("off-center calibration should be rejected")
	}
}

func joyConReport(buttons uint32) []byte {
	b := make([]byte, 64)
	binary.LittleEndian.PutUint32(b[0:], 42)
	binary.LittleEndian.PutUint32(b[4:], buttons)
	copy(b[10:], packStick(1000, 3000))
	copy(b[13:], packStick(2048, 2048))
	binary.LittleEndian.PutUint16(b[31:], 3900)
	binary.LittleEndian.PutUint16(b[48:], uint16(0xFFFF&-4096))
	binary.LittleEndian.PutUint16(b[54:], 100)
	binary.LittleEndian.PutUint16(b[58:], uint16(0xFFFF&-50))
	return b
}

func TestParseReportJoyCon(t *testing.T) {
	r, err := ParseReport(joyConReport(BtnA|BtnZL|0xFC000000), ParseOptions{ProductID: JoyCon2RightPID})
	if err != nil {
		t.Fatal(err)
	}
	if r.Buttons != BtnA|BtnZL {
		t.Fatalf("status bits should be masked: %08x", r.Buttons)
	}
	if r.Time != 42 || r.LeftStickRaw != [2]int{1000, 3000} || r.RightStickRaw != [2]int{2048, 2048} {
		t.Fatalf("%+v", r)
	}
	if r.BatteryVoltage != 3.9 || r.Accel[0] != -4096 || r.Gyro[0] != 100 || r.Gyro[2] != -50 {
		t.Fatalf("sensors %+v", r)
	}
	if _, err := ParseReport(make([]byte, 20), ParseOptions{}); err != ErrShortReport {
		t.Fatalf("short report err %v", err)
	}
}

func gcReport(b1, b2, b3, lt, rt byte) []byte {
	b := make([]byte, 64)
	b[2], b[3], b[4] = b1, b2, b3
	copy(b[5:], packStick(2048, 2048))
	copy(b[8:], packStick(2048, 2048))
	b[12], b[13] = lt, rt
	return b
}

func TestParseReportGameCube(t *testing.T) {
	opt := ParseOptions{ProductID: NSOGameCubeControllerPID, GCTriggerMode: GCTriggerBump}
	r, err := ParseReport(gcReport(0x02|0x40, 0x08, 0x01, 36, 190), opt)
	if err != nil {
		t.Fatal(err)
	}
	want := BtnA | BtnPlus | BtnUp | BtnHome | BtnZR
	if r.Buttons != want {
		t.Fatalf("buttons %08x want %08x", r.Buttons, want)
	}
	if r.LeftTrigger != 0 || r.RightTrigger != 255 {
		t.Fatalf("triggers %d/%d", r.LeftTrigger, r.RightTrigger)
	}

	// Trigger click in bump mode also reports GC_R_CLICK.
	r, _ = ParseReport(gcReport(0x10, 0, 0, 0, 240), opt)
	if r.Buttons&BtnGCRClick == 0 || r.Buttons&BtnZR == 0 {
		t.Fatalf("click: %08x", r.Buttons)
	}
	// ...but not in max mode.
	opt.GCTriggerMode = GCTriggerMax
	r, _ = ParseReport(gcReport(0x10, 0, 0, 0, 240), opt)
	if r.Buttons&BtnGCRClick != 0 {
		t.Fatalf("max mode click: %08x", r.Buttons)
	}
	// Hair trigger: 5% of travel is full press.
	opt.GCTriggerMode = GCTriggerHair
	r, _ = ParseReport(gcReport(0, 0, 0, 47, 40), opt)
	if r.LeftTrigger != 255 || r.RightTrigger != 0 {
		t.Fatalf("hair trigger %d/%d", r.LeftTrigger, r.RightTrigger)
	}
}

func TestRumblePacket(t *testing.T) {
	f := [3]Vibration{SilentVibration(), SilentVibration(), SilentVibration()}
	jc := RumblePacket(0x13, f, false)
	if len(jc) != 17 || jc[0] != 0 || jc[1] != 0x53 {
		t.Fatalf("joycon packet %x", jc)
	}
	pro := RumblePacket(2, f, true)
	if len(pro) != 33 || !bytes.Equal(pro[1:17], pro[17:33]) {
		t.Fatalf("pro packet %x", pro)
	}
	if VibrationUUID(JoyCon2LeftPID) != VibrationWriteJoyConLUUID ||
		VibrationUUID(NSOGameCubeControllerPID) != VibrationWriteProControllerUUID {
		t.Fatal("vibration UUID routing")
	}
}

func TestFromMotors(t *testing.T) {
	v := FromMotors(255, 128, 5)
	if v.LFAmp != 796 || v.HFAmp != 400 {
		t.Fatalf("got %d/%d", v.LFAmp, v.HFAmp)
	}
	if v := FromMotors(255, 255, 10); v.LFAmp != MaxAmplitude {
		t.Fatalf("strength 10 should clamp: %d", v.LFAmp)
	}
	if v := FromMotors(255, 255, 0); v.LFAmp != 0 || v.HFAmp != 0 {
		t.Fatalf("strength 0 should silence: %+v", v)
	}
}

func TestButtonNames(t *testing.T) {
	got := ButtonNames(BtnA | BtnHome)
	if len(got) != 2 || got[0] != "A" || got[1] != "HOME" {
		t.Fatal(got)
	}
}

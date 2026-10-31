package dualsense

import (
	"encoding/binary"
	"errors"
	"math"
)

// MotionCalibration converts raw device axes to g and degrees per second.
// Coefficients follow feature report 0x05 as used by Linux hid-playstation.
type MotionCalibration struct {
	accelBias, accelScale [3]float64
	gyroScale             [3]float64
}

func ParseMotionCalibration(report []byte, bluetooth bool) (MotionCalibration, error) {
	var c MotionCalibration
	if len(report) != 41 || report[0] != 0x05 {
		return c, errors.New("invalid DualSense calibration report")
	}
	if bluetooth && binary.LittleEndian.Uint32(report[37:]) != bluetoothCRC(0xa3, report[:37]) {
		return c, errors.New("invalid DualSense calibration CRC")
	}
	value := func(i int) float64 { return float64(int16(binary.LittleEndian.Uint16(report[i:]))) }
	speed := value(19) + value(21)
	if speed <= 0 {
		return c, errors.New("invalid gyroscope speed calibration")
	}
	for i := range 3 {
		bias := value(1 + 2*i)
		span := math.Abs(value(7+4*i)-bias) + math.Abs(value(9+4*i)-bias)
		plus, minus := value(23+4*i), value(25+4*i)
		range2g := plus - minus
		if span <= 0 || range2g <= 0 {
			return MotionCalibration{}, errors.New("invalid sensor calibration range")
		}
		c.gyroScale[i] = speed / span
		c.accelBias[i] = (plus + minus) / 2
		c.accelScale[i] = 2 / range2g
	}
	return c, nil
}

// Convert preserves the controller's native axes; DSU axis conversion is separate.
func (c MotionCalibration) Convert(gyro, accel [3]int16) (gyroDPS, accelG [3]float32) {
	for i := range 3 {
		gyroDPS[i] = float32(float64(gyro[i]) * c.gyroScale[i])
		accelG[i] = float32((float64(accel[i]) - c.accelBias[i]) * c.accelScale[i])
	}
	return
}

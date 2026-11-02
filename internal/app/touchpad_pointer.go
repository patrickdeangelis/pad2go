package app

import (
	"time"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

const (
	touchpadWidth             = 1920.0
	touchpadHeight            = 1080.0
	touchpadHorizontalDegrees = 90.0
	touchpadVerticalDegrees   = 60.0
	// Approximate Eden pointer response measured from the synchronized capture:
	// normalized cursor delta per normalized touch delta, [screen X/Y][touch X/Y].
	touchpadResponseXX = 2.442
	touchpadResponseXY = 0.119
	touchpadResponseYX = 0.346
	touchpadResponseYY = 2.528
)

// TouchpadPointerState converts one DualSense contact into DSU gyro motion.
type TouchpadPointerState struct {
	id          uint8
	last        protocol.TouchContact
	lastAt      time.Time
	initialized bool
}

// Update returns synthetic gyro-only motion while the first contact is held.
// A new contact is an anchor only; release returns physical motion unchanged.
func (p *TouchpadPointerState) Update(contacts [2]protocol.TouchContact, now time.Time, physical mapping.Motion) (mapping.Motion, bool) {
	touch := contacts[0]
	if !touch.Active {
		*p = TouchpadPointerState{}
		return physical, false
	}
	if !p.initialized {
		*p = TouchpadPointerState{
			id: touch.ID, last: touch, lastAt: now, initialized: true,
		}
		return mapping.Motion{}, true
	}
	if p.id != touch.ID {
		p.id, p.last, p.lastAt = touch.ID, touch, now
		return mapping.Motion{}, true
	}

	dx := float64(int(touch.X) - int(p.last.X))
	dy := float64(int(touch.Y) - int(p.last.Y))
	gap := now.Sub(p.lastAt)
	elapsed := gap.Seconds()
	p.last, p.lastAt = touch, now
	if elapsed <= 0 {
		return mapping.Motion{}, true
	}

	yawDelta := dx * touchpadHorizontalDegrees / touchpadWidth
	pitchDelta := -dy * touchpadVerticalDegrees / touchpadHeight
	gyro := [3]float32{float32(pitchDelta / elapsed), float32(yawDelta / elapsed), 0}
	return mapping.Motion{Gyro: gyro}, true
}

// calibrateTouchpadPointerMotion applies the inverse of the observed game
// response so normalized touch deltas produce normalized cursor deltas. This
// is applied only to touch-generated DSU motion; physical gyro and virtual-pad
// motion remain unchanged.
func calibrateTouchpadPointerMotion(motion mapping.Motion) mapping.Motion {
	determinant := touchpadResponseXX*touchpadResponseYY - touchpadResponseXY*touchpadResponseYX
	if determinant == 0 {
		return motion
	}
	touchX := float64(motion.Gyro[1]) / touchpadHorizontalDegrees
	touchY := -float64(motion.Gyro[0]) / touchpadVerticalDegrees
	correctedX := (touchpadResponseYY*touchX - touchpadResponseXY*touchY) / determinant
	correctedY := (-touchpadResponseYX*touchX + touchpadResponseXX*touchY) / determinant
	motion.Gyro[1] = float32(correctedX * touchpadHorizontalDegrees)
	motion.Gyro[0] = float32(-correctedY * touchpadVerticalDegrees)
	return motion
}

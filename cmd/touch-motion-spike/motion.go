package main

import (
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

// converter is the production app conversion, exposed here for deterministic
// and live Eden validation with synthetic or physical contacts.
type converter struct {
	state app.TouchpadPointerState
}

func (c *converter) sample(contacts [2]protocol.TouchContact, now time.Time) mapping.Motion {
	motion, _ := c.state.Update(contacts, now, mapping.Motion{})
	return motion
}

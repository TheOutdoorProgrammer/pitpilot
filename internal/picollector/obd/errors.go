package obdcollector

import (
	"context"
	"errors"
	"io"

	"github.com/RyoheiHashimoto/obd2"
	"github.com/RyoheiHashimoto/obd2/elm327"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	"go.bug.st/serial"
)

func sampleError(ctx context.Context, stage string, err error) error {
	// Canceling a sample closes the port; report the deadline rather than the resulting I/O error.
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	reason := diag.ErrorType(err)
	var portError *serial.PortError
	var refusal *obd2.NegativeResponseError
	switch {
	case errors.Is(err, obd2.ErrNoResponse):
		reason = "no_response"
	case errors.Is(err, elm327.ErrAdapter):
		reason = "adapter_error"
	case errors.As(err, &refusal):
		reason = "ecu_refused"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrClosedPipe):
		reason = "disconnected"
	case errors.As(err, &portError):
		reason = "serial_error"
	}
	return diag.NewError("obd."+stage+"."+reason, err)
}

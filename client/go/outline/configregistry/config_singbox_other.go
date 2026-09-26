//go:build !android && !singboxtest

package configregistry

import (
	"context"
	"errors"
)

// Sing-box support is built only into the Android client.
func NewSingboxTransportPairSubParser() func(context.Context, map[string]any) (*TransportPair, error) {
	return func(context.Context, map[string]any) (*TransportPair, error) {
		return nil, errors.ErrUnsupported
	}
}

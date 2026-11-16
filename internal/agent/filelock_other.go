//go:build !linux && !darwin

package agent

import (
	"context"
	"errors"
)

func lockState(context.Context, string, string) (func(), error) {
	return nil, errors.New("plataforma sem suporte ao bloqueio")
}

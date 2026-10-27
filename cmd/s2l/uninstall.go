package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/validation"
)

// Never use Store.New here: uninstall inspection must not recover or rewrite state.
func uninstallMappings(path string) ([]model.TunnelMapping, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state model.AppState
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) || json.Unmarshal(data, &state) != nil || state.Nodes == nil || state.Mappings == nil {
		return nil, fmt.Errorf("invalid state; restore a valid backup before uninstalling")
	}
	if err := validation.State(state); err != nil {
		return nil, err
	}
	return state.Mappings, nil
}

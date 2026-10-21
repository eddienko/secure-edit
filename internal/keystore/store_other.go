//go:build !darwin

package keystore

func newStore() Store { return nil }

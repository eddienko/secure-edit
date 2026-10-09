//go:build !darwin

package main

func newStore() PasswordStore { return nil }

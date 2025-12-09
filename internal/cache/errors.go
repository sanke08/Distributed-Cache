package cache

import "errors"

var (
	ErrUserNotFound = errors.New("user not found")
	ErrKeyNotFound  = errors.New("key not found")
	ErrUserExists   = errors.New("user exists")
)

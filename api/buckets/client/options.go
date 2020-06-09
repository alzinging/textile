package client

import "github.com/ipfs/interface-go-ipfs-core/path"

type options struct {
	root     path.Resolved
	password string
	progress chan<- int64
}

type Option func(*options)

// WithFastForwardOnly instructs the remote to reject non-fast-forward updates by comparing root with the remote.
func WithFastForwardOnly(root path.Resolved) Option {
	return func(args *options) {
		args.root = root
	}
}

// WithPassword encrypts the file with a password.
func WithPassword(password string) Option {
	return func(args *options) {
		args.password = password
	}
}

// WithProgress writes progress updates to the given channel.
func WithProgress(ch chan<- int64) Option {
	return func(args *options) {
		args.progress = ch
	}
}

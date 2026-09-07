// Package secret keeps long-lived credentials out of config files. A Store is
// the only thing the rest of clouder knows about a token; the default backend
// is pass(1), but any implementation of the interface will do.
package secret

// Store reads and writes named secrets.
type Store interface {
	// Get returns the first line of the secret at key.
	Get(key string) (string, error)
	// Set stores value under key, overwriting any previous value.
	Set(key, value string) error
	// Delete removes the secret at key. A missing key is not an error.
	Delete(key string) error
	// Keys lists the secret names directly under prefix (no recursion), each as
	// the full key. A missing prefix yields an empty list.
	Keys(prefix string) ([]string, error)
}

// Key builds the storage key "clouder/<provider>/<account>".
func Key(provider, account string) string {
	return "clouder/" + provider + "/" + account
}

// Prefix builds "clouder/<provider>", the parent of every account key.
func Prefix(provider string) string {
	return "clouder/" + provider
}

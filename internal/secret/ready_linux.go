package secret

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	secretService   = "org.freedesktop.secrets"
	secretsPath     = dbus.ObjectPath("/org/freedesktop/secrets")
	loginCollection = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
	defaultAlias    = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
)

// ready checks, before the library is called, that Secret Service can answer without a person at a
// screen. The library starts a session bus with dbus-launch when it finds none, and it asks to unlock the
// collection at every access, which waits until someone answers the prompt. Neither helps a session
// without a desktop, so both end here at once: without a session bus the store is unavailable, and a
// locked collection whose prompt nobody can answer is locked. Everything else is left to the library.
func ready(ctx context.Context) error {
	conn, err := dbus.SessionBusPrivateNoAutoStartup(dbus.WithContext(ctx))
	if err != nil {
		return ErrUnavailable
	}
	defer conn.Close()
	if conn.Auth(nil) != nil || conn.Hello() != nil {
		return ErrUnavailable
	}
	locked, err := collectionLocked(ctx, conn)
	switch {
	case err != nil:
		return ErrUnavailable
	case locked && !promptable(ctx):
		return fmt.Errorf("%w: %w", ErrUnavailable, ErrLocked)
	}
	return nil
}

// collectionLocked reads the Locked property of the collection the library uses: the login collection
// where there is one, otherwise the default alias.
func collectionLocked(ctx context.Context, conn *dbus.Conn) (bool, error) {
	collections, err := property(ctx, conn.Object(secretService, secretsPath),
		"org.freedesktop.Secret.Service", "Collections")
	if err != nil {
		return false, err
	}
	path := defaultAlias
	if paths, ok := collections.([]dbus.ObjectPath); ok {
		for _, p := range paths {
			if p == loginCollection {
				path = loginCollection
			}
		}
	}
	value, err := property(ctx, conn.Object(secretService, path), "org.freedesktop.Secret.Collection", "Locked")
	if err != nil {
		return false, err
	}
	locked, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("the Locked property is not a boolean")
	}
	return locked, nil
}

func property(ctx context.Context, object dbus.BusObject, iface, name string) (any, error) {
	var value dbus.Variant
	err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, name).Store(&value)
	return value.Value(), err
}

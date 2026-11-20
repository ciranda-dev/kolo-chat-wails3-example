package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	kololog "github.com/ciranda-dev/kolo-log"
	"github.com/ciranda-dev/kolo-pin"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	kolostore "github.com/ciranda-dev/kolo-store"
	kolosync "github.com/ciranda-dev/kolo-sync"
)

// pinTimeout bounds one conversation with the pin: connecting, pushing or pulling.
const pinTimeout = 30 * time.Second

// capabilities returns the signed tokens that let each pin keep our logs, the messages and the directory, in
// the order of the pins. A capability names one pin, so each pin has its own.
func capabilities(set settings, id *koloid.Identity, logs ...kololog.LogID) ([][]byte, error) {
	out := make([][]byte, len(set.pins))
	for i, p := range set.pins {
		token, err := capability(set, id, p, logs...)
		if err != nil {
			return nil, err
		}
		out[i] = token
	}
	return out, nil
}

// capability returns the token for one pin. A token from the configuration is checked and used as it is.
// Otherwise the app issues one with its own identity, for the pin and these two logs, keeps it in the data
// directory and reuses it until half of its lifetime has passed, since the pin refuses a second capability
// issued in the same second as the first. A token from step 4 covers only the messages, so it is replaced by
// one for both.
func capability(set settings, id *koloid.Identity, p pinRef, logs ...kololog.LogID) ([]byte, error) {
	if token, ok := set.tokens[string(p.Key)]; ok {
		return token, checkToken(token, id, p.Key, logs...)
	}
	dir := filepath.Join(set.DataDir, "pin-tokens")
	path := filepath.Join(dir, hex.EncodeToString(p.Key))
	if token, err := readFile(path, maxStateFile); err == nil {
		c, err := pin.ParseCapability(token)
		if err == nil && c.Quota == set.PinQuota && time.Until(c.Expires) > set.PinTTL/2 &&
			checkToken(token, id, p.Key, logs...) == nil {
			return token, nil
		}
	}
	token, err := pin.Issue(id, pin.Capability{
		Peer:    p.Key,
		Logs:    logs,
		Expires: time.Now().Add(set.PinTTL),
		Quota:   set.PinQuota,
	})
	if err != nil {
		return nil, &ConfigError{Setting: "pin-ttl", Err: err}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, token, 0o600); err != nil {
		return nil, err
	}
	return token, nil
}

// checkToken says why a token cannot work, with the errors of kolo-pin: ErrInvalidCapability for bytes that are
// not a capability signed by its owner, ErrUnauthorized for one that is not from this identity, for this pin and
// for these logs, and ErrExpired for one that has expired. The pin checks all of it again when it is used.
func checkToken(token []byte, id *koloid.Identity, pinKey ed25519.PublicKey, logs ...kololog.LogID) error {
	c, err := pin.ParseCapability(token)
	switch {
	case err != nil:
		err = fmt.Errorf("%w: %w", pin.ErrInvalidCapability, err)
	case !c.Owner.Equal(id.PublicKey()):
		err = fmt.Errorf("%w: issued by another identity", pin.ErrUnauthorized)
	case !c.Peer.Equal(pinKey):
		err = fmt.Errorf("%w: issued for another pin", pin.ErrUnauthorized)
	case slices.ContainsFunc(logs, func(id kololog.LogID) bool { return !c.Covers(id) }):
		err = fmt.Errorf("%w: does not cover this conversation's logs", pin.ErrUnauthorized)
	case !time.Now().Before(c.Expires):
		err = fmt.Errorf("%w: at %s", pin.ErrExpired, c.Expires.Format(time.RFC3339))
	}
	if err != nil {
		return &ConfigError{Setting: "pin-token", Err: err}
	}
	return nil
}

// pushToPin gives every pin our two logs, the messages and the directory, which each pulls and verifies from
// what it does not have yet. It is how every message and every change of the directory reaches the pins, and
// it can be repeated at any time. The pins are pushed to at once, each with its own timeout, so one that does
// not answer delays no other. The error joins those of the pins that failed, each an error of kolo-pin:
// pin.ErrExpired, pin.ErrUnauthorized, pin.ErrRevoked, pin.ErrQuotaExceeded, pin.ErrDiskFull,
// pin.ErrUnreachable.
func (c *ChatService) pushToPin(ctx context.Context) error {
	c.pushMu.Lock() // a pin answers busy to a second Add of the same log
	defer c.pushMu.Unlock()
	errs := make([]error, len(c.set.pins))
	var wg sync.WaitGroup
	for i, p := range c.set.pins {
		wg.Go(func() { errs[i] = c.pushTo(ctx, p, c.tokens[i]) })
	}
	wg.Wait()
	c.setPin(func(s []pinState) {
		for i, err := range errs {
			s[i].Push = describe(err, c.set.pins[i].Addr)
		}
	})
	return pinErrors(c.set.pins, errs)
}

func (c *ChatService) pushTo(ctx context.Context, p pinRef, token []byte) error {
	ctx, cancel := context.WithTimeout(ctx, pinTimeout)
	defer cancel()
	var errs []error
	for _, l := range []*kolostore.Log{c.log, c.dirLog} {
		if _, err := pin.Add(ctx, c.id, p.peer(), l, token); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// pullFromPin brings the replica of every known peer's log, and of its directory, up to date from every pin.
// The pins are pulled from at once, each with its own timeout, so one that does not answer delays no other; the
// pulls themselves take turns, since a replica takes one at a time (pullMu). A pin that is behind another
// changes nothing, and one that does not have a log is skipped, so the replicas end with the longest history
// any pin holds. The pins send ciphertext only, and each pull derives the keys of what it brought (see
// entrykeys.go), so the sender need not be online.
func (c *ChatService) pullFromPin(ctx context.Context) error {
	authors := slices.DeleteFunc(c.authors(), func(a string) bool { return !c.isAccepted(a) }) // the roster only
	if len(authors) == 0 {
		return nil
	}
	errs := make([]error, len(c.set.pins))
	var wg sync.WaitGroup
	for i, p := range c.set.pins {
		wg.Go(func() { errs[i] = c.pullFrom(ctx, p, authors) })
	}
	wg.Wait()
	c.setPin(func(s []pinState) {
		for i, err := range errs {
			s[i].Pull = describe(err, c.set.pins[i].Addr)
		}
	})
	return pinErrors(c.set.pins, errs)
}

// pullFrom pulls every author's two logs from one pin.
func (c *ChatService) pullFrom(ctx context.Context, p pinRef, authors []string) error {
	ctx, cancel := context.WithTimeout(ctx, pinTimeout)
	defer cancel()
	conn, err := kolosecure.Dial(ctx, p.Addr, c.id, p.Key, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", pin.ErrUnreachable, err)
	}
	defer conn.Close()
	var errs []error
	for _, author := range authors {
		if err := c.pullPinnedMessages(ctx, conn, author); err != nil {
			errs = append(errs, fmt.Errorf("pin pull: %w", err))
		}
		err := c.pullDirectory(ctx, conn, author)
		if err != nil && !errors.Is(err, kolosync.ErrNotFound) && !errors.Is(err, errNoDirectory) {
			errs = append(errs, fmt.Errorf("pin pull of directory: %w", err))
		}
	}
	return errors.Join(errs...)
}

// pinErrors joins the errors of the pins that failed, each with the pin's address.
func pinErrors(pins []pinRef, errs []error) error {
	var out []error
	for i, err := range errs {
		if err != nil {
			out = append(out, fmt.Errorf("pin %s: %w", pins[i].Addr, err))
		}
	}
	return errors.Join(out...)
}

// pullPinnedMessages pulls the ciphertext of one author's messages from the pin and shows what can be read.
func (c *ChatService) pullPinnedMessages(ctx context.Context, conn kolosync.Conn, author string) error {
	rep, err := c.replica(author)
	if err == nil {
		err = c.pull(ctx, conn, author, rep)
	}
	if errors.Is(err, kolosync.ErrNotFound) {
		return nil // the author has not pinned anything yet
	}
	if err != nil {
		return err
	}
	c.markPinned(author, rep.Len())
	c.show(author)
	return nil
}

// describe turns an error of the pin at addr into the sentence the window shows, and "" for no error.
func describe(err error, addr string) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, pin.ErrExpired):
		return "The token for the pin at " + addr + " has expired, so new messages are not pinned there. Restart the app to issue a new one, or set a new -pin-token. Messages still go directly to peers who are online."
	case errors.Is(err, pin.ErrQuotaExceeded):
		return "The quota is full on the pin at " + addr + ", so new messages are not pinned there. Raise -pin-quota and restart. Messages still go directly to peers who are online."
	case errors.Is(err, pin.ErrDiskFull):
		return "The pin at " + addr + " has no room left, so new messages are not pinned there. Ask its operator for more space."
	case errors.Is(err, pin.ErrUnauthorized):
		return "The pin at " + addr + " refused this identity or token. Check -pin and -pin-token, and ask the operator whether your key is allowed."
	case errors.Is(err, pin.ErrRevoked):
		return "The token for the pin at " + addr + " was revoked or replaced by a newer one."
	case errors.Is(err, pin.ErrUnreachable):
		return "The pin at " + addr + " is unreachable. Messages still go directly to peers who are online."
	}
	return "Pin " + addr + ": " + err.Error()
}

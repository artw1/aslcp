// SPDX-License-Identifier: GPL-3.0-or-later

package m17

import (
	"errors"
	"fmt"
	"strings"
)

// Alphabet is the base-40 character set: index 0 is space, then A-Z, 0-9,
// '-', '/', '.'.
const Alphabet = " ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-/."

// MaxCallsign is the longest encodable callsign.
const MaxCallsign = 9

// Address ranges (spec, Address Encoding appendix).
const (
	// AddrReserved (0) is reserved for future use.
	AddrReserved Address = 0
	// AddrStandardMax is the last standard address (".........").
	AddrStandardMax Address = 0xEE6B27FFFFFF
	// AddrExtendedMin is the first extended (application use) address.
	AddrExtendedMin Address = 0xEE6B28000000
	// AddrBroadcast is the broadcast address, valid only as a destination.
	AddrBroadcast Address = 0xFFFFFFFFFFFF
)

// BroadcastName is how DecodeCallsign renders AddrBroadcast.
const BroadcastName = "BROADCAST"

// Callsign errors.
var (
	ErrEmptyCallsign  = errors.New("m17: empty callsign")
	ErrCallsignLength = errors.New("m17: callsign longer than 9 characters")
	ErrCallsignChar   = errors.New("m17: invalid character in callsign")
	ErrCallsignSpace  = errors.New("m17: callsign ends with a space")
	ErrAddrReserved   = errors.New("m17: reserved address")
	ErrAddrExtended   = errors.New("m17: extended (non-callsign) address")
	ErrAddrOutOfRange = errors.New("m17: address does not fit 48 bits")
)

// Address is a 48-bit M17 address.
type Address uint64

// Bytes returns the 6-byte big endian form.
func (a Address) Bytes() [6]byte {
	return [6]byte{byte(a >> 40), byte(a >> 32), byte(a >> 24), byte(a >> 16), byte(a >> 8), byte(a)}
}

// AddressFromBytes reads a 6-byte big endian address.
func AddressFromBytes(b []byte) Address {
	_ = b[5]
	return Address(b[0])<<40 | Address(b[1])<<32 | Address(b[2])<<24 | Address(b[3])<<16 | Address(b[4])<<8 | Address(b[5])
}

// String renders the address as a callsign, or "BROADCAST", or hex for
// reserved and extended addresses.
func (a Address) String() string {
	s, err := DecodeCallsign(a)
	if err != nil {
		return fmt.Sprintf("0x%012X", uint64(a))
	}
	return s
}

// ValidateCallsign reports whether s can be encoded: 1..9 characters from
// the alphabet (lowercase letters are accepted and folded) and no trailing
// space.
func ValidateCallsign(s string) error {
	_, err := EncodeCallsign(s)
	return err
}

// EncodeCallsign encodes a callsign of up to 9 characters of
// "A-Z0-9 -/." (lowercase folded to uppercase) into an address. The result
// is never reserved, extended or broadcast. Trailing spaces are rejected
// because they would not survive a decode.
func EncodeCallsign(s string) (Address, error) {
	if s == "" {
		return 0, ErrEmptyCallsign
	}
	if len(s) > MaxCallsign {
		return 0, ErrCallsignLength
	}
	s = strings.ToUpper(s)
	var a Address
	for i := len(s) - 1; i >= 0; i-- {
		idx := strings.IndexByte(Alphabet, s[i])
		if idx < 0 {
			return 0, fmt.Errorf("%w %q", ErrCallsignChar, s[i])
		}
		if i == len(s)-1 && idx == 0 {
			return 0, ErrCallsignSpace
		}
		a = a*40 + Address(idx)
	}
	return a, nil
}

// DecodeCallsign decodes a standard address. AddrBroadcast returns
// BroadcastName with a nil error; the reserved address and the extended
// range return ErrAddrReserved / ErrAddrExtended, and anything above 48
// bits ErrAddrOutOfRange.
func DecodeCallsign(a Address) (string, error) {
	switch {
	case a > AddrBroadcast:
		return "", ErrAddrOutOfRange
	case a == AddrBroadcast:
		return BroadcastName, nil
	case a == AddrReserved:
		return "", ErrAddrReserved
	case a >= AddrExtendedMin:
		return "", ErrAddrExtended
	}
	var b []byte
	for a > 0 {
		b = append(b, Alphabet[a%40])
		a /= 40
	}
	return string(b), nil
}

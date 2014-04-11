// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by a
// license that can be found in the LICENSE file.

package validation

import (
	"fmt"
	"net"
	"strings"
)

//NotEmpty check to see if the value is not an empty string or a string with just whitespace characters, returns an
// error if empty. FieldName is used to create a meaningful error
func NotEmpty(fieldName string, value string) error {
	if strings.TrimSpace(value) == "" {
		return NewViolation(fmt.Sprintf("empty string for %v", fieldName))
	}
	return nil
}

//IsIP checks to see if the value is a valid IP address. Returns an error if not valid
func IsIP(value string) error {
	if nil == net.ParseIP(value) {
		return NewViolation(fmt.Sprintf("invalid IP Address %s", value))
	}
	return nil
}

//StringsEqual checks to see that strings are equal, optional msg to use instead of default
func StringsEqual(expected string, other string, errMsg string) error {
	if expected != other {
		if errMsg == "" {
			errMsg = fmt.Sprintf("expected %s found %s", expected, other)
		}
		return NewViolation(errMsg)
	}
	return nil
}
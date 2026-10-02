//go:build !integration && !e2e

package jsonstrict

import (
	"errors"
	"strings"
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

func TestDecode(t *testing.T) {
	type money struct {
		Amount string `json:"amount"`
	}
	type body struct {
		Name  string `json:"name"`
		Money money  `json:"money"`
	}
	cases := map[string]struct {
		in   string
		code wagering.FailureCode
	}{
		"ok":             {`{"name":"x","money":{"amount":"1.00"}}`, ""},
		"numeric amount": {`{"money":{"amount":1.0}}`, wagering.FailureInvalidMoney},
		"wrong type":     {`{"name":1}`, wagering.FailureInvalidField},
		"unknown field":  {`{"extra":1}`, wagering.FailureMalformedPayload},
		"trailing data":  {`{"name":"x"} {}`, wagering.FailureMalformedPayload},
		"not json":       {`{`, wagering.FailureMalformedPayload},
		"empty":          {``, wagering.FailureMalformedPayload},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var v body
			err := Decode(strings.NewReader(tc.in), &v)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var in *wagering.InputError
			if !errors.As(err, &in) || in.Code() != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

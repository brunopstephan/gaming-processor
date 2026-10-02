package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/jsonstrict"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

const maxBodyBytes = 1 << 20

func inputError(code wagering.FailureCode, field, reason string) *wagering.InputError {
	return &wagering.InputError{Violations: []wagering.Violation{{Code: code, Field: field, Reason: reason}}}
}

// decodeJSON strictly decodes one JSON object into v: unknown fields, trailing
// data, oversized bodies and wrong JSON types are correctable input errors.
// A numeric money amount is INVALID_MONEY: amounts are decimal strings.
func decodeJSON(c *gin.Context, v any) error {
	return jsonstrict.Decode(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes), v)
}

// parseUUIDParam reads a lowercase canonical UUID path parameter.
func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	raw := c.Param(name)
	id, err := uuid.Parse(raw)
	if err != nil || id.String() != raw {
		return uuid.Nil, inputError(wagering.FailureInvalidID, name, "must be a lowercase canonical UUID")
	}
	return id, nil
}

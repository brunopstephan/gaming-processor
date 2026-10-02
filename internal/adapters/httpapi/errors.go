package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

type violationDTO struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

type errorDetail struct {
	FailureCode string         `json:"failureCode"`
	Category    string         `json:"category,omitempty"`
	Message     string         `json:"message"`
	Details     []violationDTO `json:"details,omitempty"`
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

func failure(code, category, message string) errorBody {
	return errorBody{Error: errorDetail{FailureCode: code, Category: category, Message: message}}
}

// errorResponse maps application errors to the HTTP contract. Transient
// errors are checked first: a retryable failure must never surface as 409.
func errorResponse(err error) (int, errorBody) {
	var input *wagering.InputError
	switch {
	case errors.Is(err, app.ErrTransient):
		return http.StatusServiceUnavailable, failure("TEMPORARILY_UNAVAILABLE", "", "temporarily unavailable, retry later")
	case errors.As(err, &input):
		body := failure(string(input.Code()), string(wagering.CategoryCorrectable), "invalid request")
		for _, v := range input.Violations {
			body.Error.Details = append(body.Error.Details, violationDTO{Field: v.Field, Code: string(v.Code), Reason: v.Reason})
		}
		return http.StatusBadRequest, body
	case errors.Is(err, app.ErrIdempotencyKeyConflict):
		return http.StatusConflict, failure("IDEMPOTENCY_KEY_CONFLICT", string(wagering.CategoryDefinitive), "idempotency key reused with a different payload")
	case errors.Is(err, app.ErrExternalTransactionConflict):
		return http.StatusConflict, failure("EXTERNAL_TRANSACTION_CONFLICT", string(wagering.CategoryDefinitive), "external transaction already registered under another idempotency key")
	case errors.Is(err, app.ErrWalletAlreadyExists):
		return http.StatusConflict, failure("WALLET_ALREADY_EXISTS", string(wagering.CategoryDefinitive), "wallet already exists for player and currency")
	case errors.Is(err, app.ErrNotFound):
		return http.StatusNotFound, failure("NOT_FOUND", "", "resource not found")
	case errors.Is(err, app.ErrInvalidCursor):
		return http.StatusBadRequest, failure("INVALID_CURSOR", string(wagering.CategoryCorrectable), "invalid ledger cursor")
	case errors.Is(err, app.ErrInvalidLimit):
		return http.StatusBadRequest, failure("INVALID_LIMIT", string(wagering.CategoryCorrectable), "limit must be between 1 and 200")
	}
	return http.StatusInternalServerError, failure("INTERNAL_ERROR", "", "internal error")
}

// writeError answers err and logs unexpected failures (without request bodies).
func writeError(c *gin.Context, err error) {
	status, body := errorResponse(err)
	if status == http.StatusServiceUnavailable {
		c.Header("Retry-After", "1")
	}
	if status == http.StatusInternalServerError {
		logger(c).ErrorContext(c.Request.Context(), "request failed",
			"correlationId", correlationID(c), "route", c.FullPath(), "error", err.Error())
	}
	c.AbortWithStatusJSON(status, body)
}

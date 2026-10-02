package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

type transactionHandlers struct {
	wagering *app.WageringService
	queries  *app.QueryService
}

// process applies an operation for the authenticated provider. The provider
// in the body must be the token's provider: otherwise 403 and no effect.
func (h transactionHandlers) process(c *gin.Context) {
	p := principal(c)
	var req transactionRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, err)
		return
	}
	if p.ProviderID == "" || (req.ProviderID != "" && req.ProviderID != p.ProviderID) {
		forbidden(c, "providerId does not match the authenticated provider")
		return
	}
	cmd, err := wagering.ParseCommand(wagering.RawCommand{
		ProviderID: req.ProviderID, ExternalTransactionID: req.ExternalTransactionID,
		IdempotencyKey: c.GetHeader("Idempotency-Key"), PlayerID: req.PlayerID, WalletID: req.WalletID,
		RoundID: req.RoundID, GameID: req.GameID, Kind: req.Kind,
		Amount: req.Money.Amount, Currency: req.Money.Currency,
		ReferenceExternalTransactionID: req.ReferenceExternalTransactionID,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	res, err := h.wagering.Process(c.Request.Context(), cmd, meta(c), nil)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(statusCode(res.Transaction.Status()), newProcessResponse(res.Transaction, res.Replay))
}

// get reads by internal id: providers see only their own operations (others
// are 404); the internal client (wagering:read) sees everything.
func (h transactionHandlers) get(c *gin.Context) {
	id, err := parseUUIDParam(c, "transactionId")
	if err != nil {
		writeError(c, err)
		return
	}
	p := principal(c)
	viewer := app.Viewer{ProviderID: p.ProviderID, Internal: p.Has(auth.ScopeWageringRead)}
	t, err := h.queries.Transaction(c.Request.Context(), id, viewer)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newTransactionView(t))
}

// getByExternalID serves the provider route; the path provider must be the caller.
func (h transactionHandlers) getByExternalID(c *gin.Context) {
	p := principal(c)
	if c.Param("providerId") != p.ProviderID || p.ProviderID == "" {
		forbidden(c, "providerId does not match the authenticated provider")
		return
	}
	t, err := h.queries.TransactionByExternalID(c.Request.Context(), p.ProviderID, c.Param("externalTransactionId"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newTransactionView(t))
}

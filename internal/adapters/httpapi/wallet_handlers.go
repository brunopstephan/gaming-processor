package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

type walletHandlers struct {
	wallets        *app.WalletService
	queries        *app.QueryService
	reconciliation *app.ReconciliationService
}

func meta(c *gin.Context) app.Meta {
	return app.Meta{CorrelationID: correlationID(c), Channel: app.ChannelHTTP}
}

func (h walletHandlers) open(c *gin.Context) {
	var req struct {
		PlayerID       string       `json:"playerId"`
		InitialBalance moneyRequest `json:"initialBalance"`
	}
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, err)
		return
	}
	cmd, err := app.ParseOpenWallet(req.PlayerID, req.InitialBalance.Amount, req.InitialBalance.Currency)
	if err != nil {
		writeError(c, err)
		return
	}
	w, err := h.wallets.Open(c.Request.Context(), cmd, meta(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, newWalletResponse(w))
}

func (h walletHandlers) get(c *gin.Context) {
	id, err := parseUUIDParam(c, "walletId")
	if err != nil {
		writeError(c, err)
		return
	}
	w, err := h.queries.Wallet(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newWalletResponse(w))
}

func (h walletHandlers) ledger(c *gin.Context) {
	id, err := parseUUIDParam(c, "walletId")
	if err != nil {
		writeError(c, err)
		return
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil {
			writeError(c, app.ErrInvalidLimit)
			return
		}
	}
	page, err := h.queries.Ledger(c.Request.Context(), id, c.Query("cursor"), limit)
	if err != nil {
		writeError(c, err)
		return
	}
	resp := ledgerPageResponse{Items: make([]ledgerEntryResponse, 0, len(page.Entries)), NextCursor: page.NextCursor}
	for _, e := range page.Entries {
		resp.Items = append(resp.Items, newLedgerEntryResponse(e))
	}
	c.JSON(http.StatusOK, resp)
}

func (h walletHandlers) reconcile(c *gin.Context) {
	id, err := parseUUIDParam(c, "walletId")
	if err != nil {
		writeError(c, err)
		return
	}
	r, err := h.reconciliation.Reconcile(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, reconciliationResponse{
		WalletID: r.WalletID.String(), StoredBalance: r.Stored, CalculatedBalance: r.Calculated,
		Difference: r.Difference, Consistent: r.Consistent, CheckedEntries: r.CheckedEntries,
	})
}

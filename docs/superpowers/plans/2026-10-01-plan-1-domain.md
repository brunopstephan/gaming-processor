# Plano 1 — Fundação e Domínio Puro: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Criar o módulo Go e todo o domínio puro (Money, Wallet, LedgerEntry, WagerTransaction, regras dos 5 tipos,
hash canônico, failure codes e eventos), 100% coberto por testes unitários e sem nenhuma infraestrutura.

**Architecture:** Pacotes em `internal/domain/` sem dependência de Fx, Gin, GORM ou AWS. As entidades têm estado
privado, construtores com validação, reidratação separada da criação e transições explícitas. A regra de negócio de
cada operação mora em `WagerTransaction.Process`, que recebe a carteira já travada e a referência já resolvida pela
camada de aplicação (Plano 3). Os eventos são registrados pela entidade e extraídos com `PullEvents()`.

**Tech Stack:** Go 1.25, `github.com/google/uuid` (UUIDv7), pacote `testing` da stdlib.

**Spec:** `docs/superpowers/specs/2026-10-01-wallet-service-design.md` (seções 4, 5, 8 e 12). Enunciado: `docs/CHALLENGE.md`.

## Roadmap (5 planos)

Cada plano é escrito quando o anterior termina, usando as interfaces reais.

1. **Fundação e domínio puro** (este plano).
2. **Infra local e persistência:** docker-compose (postgres, postgres\_test, migrate, ministack e ministack\_test com
 init), migrations (tabelas, constraints, triggers, papéis `wallet_owner` e `wallet_app`), `adapters/postgres`
 (modelos GORM, repositórios, TxManager), config e módulo Fx de DB, testes de integração de migrations,
 constraints e imutabilidade.
3. **Casos de uso, HTTP e auth:** realm do Keycloak (dev e test), middleware go-oidc, casos de uso (OpenWallet,
 ProcessWagerTransaction com idempotência, FAILED, consultas, Reconcile), handlers Gin, health, logging slog e
 métricas base, ciclo de vida Fx do HTTP, testes de integração com tokens reais.
4. **Mensageria:** consumer SQS com inbox e SenderId, outbox publisher com lease, worker de referências, DLQ,
 shutdown, testes de integração.
5. **Multi-instância, observabilidade e entrega:** Dockerfile, nginx com 3 réplicas, Prometheus e Grafana
 provisionados, E2E com 3 processos e `faultinject`, README, ARCHITECTURE.md e .env.example.

## Global Constraints

- Módulo: `github.com/brunopstephan/backend-challenge-go`; `go 1.25` no `go.mod`.
- Dinheiro nunca passa por `float32`/`float64` (parsing, cálculo, serialização). `int64` em centavos, escala fixa 2.
- `internal/domain/**` só importa stdlib, `github.com/google/uuid` e outros pacotes de `internal/domain`.
- Rejeição de negócio nunca usa `panic`. Os erros são classificáveis por `errors.Is`/`errors.As`.
- Testes usam apenas `testing` (sem testify). Todo arquivo de teste unitário começa com `//go:build !integration && !e2e`.
- Código formatado com `gofmt`, e `go vet ./...` limpo.
- Toda mensagem de commit termina com a linha `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Failure codes exatamente como na spec 4.5, mais dois códigos `CORRECTABLE` que este plano acrescenta para
entradas não previstas na spec: `INVALID_FIELD` (texto acima de 255 bytes) e `REFERENCE_NOT_ALLOWED`
(BET/LOSS com `referenceExternalTransactionId`). A tabela de códigos do `ARCHITECTURE.md` deve incluí-los.

## Review Focus

1. Money em JSON com `amount` numérico (`{"amount":25.00}`) deve ser rejeitado, nunca convertido. Teste na Task 2.
2. A fronteira exata de overflow: `92233720368547758.07` é aceito e `.08` é rejeitado. Teste na Task 2.
3. `-0.00` e `+0.00` são rejeitados no parsing externo. Teste na Task 2.
4. Reidratar uma carteira com saldo negativo ou versão 0 falha. Teste na Task 5.
5. `Process` chamado de novo numa transação terminal falha com `ErrInvalidTransition` sem mover saldo. Teste na Task 10.

## File Structure

```
go.mod, go.sum, .gitignore, README.md (stub), docs/CHALLENGE.md (enunciado movido)
internal/domain/money/
  errors.go       sentinelas do pacote
  currency.go     Currency + ParseCurrency
  iso4217.go      tabela ISO 4217 de expoente 2
  money.go        Money, Parse, FromMinor, Zero, formatação, JSON
  arith.go        Add, Sub, Negate, Cmp, Equal (overflow)
internal/domain/wallet/
  errors.go       sentinelas
  ledger.go       Direction, LedgerEntry, NewLedgerEntry
  wallet.go       Wallet, Open, Rehydrate, Debit, Credit
internal/domain/events/
  events.go       Data, Envelope, NewEnvelope, FormatTime
  types.go        os 4 eventos concretos
internal/domain/wagering/
  failure.go      FailureCode, Category, Violation, InputError
  kind.go         Kind, Status, Origin, máquina de estados
  command.go      RawCommand, Command, ParseCommand, PayloadHash
  errors.go       sentinelas
  transaction.go  WagerTransaction, NewExternal, NewOpening, Rehydrate, State, transições, eventos
  retry.go        RetryPolicy
  process.go      Reference, ProcessInput, Process (regras dos 5 tipos)
```

---

### Task 1: Scaffold do módulo e Currency

**Files:**

- Move: `README.md` → `docs/CHALLENGE.md`
- Create: `README.md`, `.gitignore`, `go.mod`, `internal/domain/money/errors.go`, `internal/domain/money/currency.go`, `internal/domain/money/iso4217.go`
- Test: `internal/domain/money/currency_test.go`

**Interfaces:**

- Produces: `money.Currency` (comparável com `==`), `money.ParseCurrency(code string) (Currency, error)`, `Currency.Code() string`, `Currency.IsZero() bool`, `money.BRL`, `money.USD`; sentinelas `ErrInvalidAmount`, `ErrUnsupportedCurrency`, `ErrCurrencyMismatch`, `ErrOverflow`, `ErrUninitialized`.

- [ ] **Step 1: Mover o enunciado e criar o módulo**

```bash
cd /Users/brunopifferstephan/code/projects/backend-challenge-go
mkdir -p docs && git mv README.md docs/CHALLENGE.md
go mod init github.com/brunopstephan/backend-challenge-go
go mod edit -go=1.25
```

Criar `README.md`:

```markdown
# backend-challenge-go

Serviço de carteiras e apostas distribuído em Go. Em construção.

- Enunciado: [docs/CHALLENGE.md](docs/CHALLENGE.md)
- Design: [docs/superpowers/specs/2026-10-01-wallet-service-design.md](docs/superpowers/specs/2026-10-01-wallet-service-design.md)
```

Criar `.gitignore`:

```
/bin/
*.test
*.out
.env
```

- [ ] **Step 2: Escrever o teste que falha**

`internal/domain/money/currency_test.go`:

```go
//go:build !integration && !e2e

package money

import (
	"errors"
	"testing"
)

func TestParseCurrency(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{name: "BRL", code: "BRL"},
		{name: "USD", code: "USD"},
		{name: "EUR", code: "EUR"},
		{name: "lowercase", code: "brl", wantErr: true},
		{name: "empty", code: "", wantErr: true},
		{name: "exponent 0 (JPY)", code: "JPY", wantErr: true},
		{name: "exponent 3 (BHD)", code: "BHD", wantErr: true},
		{name: "no exponent (XAU)", code: "XAU", wantErr: true},
		{name: "unknown", code: "ZZZ", wantErr: true},
		{name: "too long", code: "BRLL", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParseCurrency(tt.code)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedCurrency) {
					t.Fatalf("ParseCurrency(%q) error = %v, want ErrUnsupportedCurrency", tt.code, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCurrency(%q) unexpected error: %v", tt.code, err)
			}
			if c.Code() != tt.code {
				t.Fatalf("Code() = %q, want %q", c.Code(), tt.code)
			}
		})
	}
}

func TestCurrencyZeroValue(t *testing.T) {
	var c Currency
	if !c.IsZero() {
		t.Fatal("zero Currency must report IsZero")
	}
	if BRL.IsZero() || USD.IsZero() {
		t.Fatal("BRL/USD must not be zero")
	}
	if BRL == USD {
		t.Fatal("BRL must differ from USD")
	}
}
```

- [ ] **Step 3: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/money/...`
Expected: FAIL de compilação, `undefined: ParseCurrency`.

- [ ] **Step 4: Implementar**

`internal/domain/money/errors.go`:

```go
package money

import "errors"

var (
	// ErrInvalidAmount reports an amount that is not in the canonical external form.
	ErrInvalidAmount = errors.New("money: invalid amount")
	// ErrUnsupportedCurrency reports a code that is not an ISO 4217 currency with exponent 2.
	ErrUnsupportedCurrency = errors.New("money: unsupported currency")
	// ErrCurrencyMismatch reports arithmetic or comparison between different currencies.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	// ErrOverflow reports a result outside the int64 minor-unit range.
	ErrOverflow = errors.New("money: overflow")
	// ErrUninitialized reports use of the zero Money or zero Currency.
	ErrUninitialized = errors.New("money: uninitialized value")
)
```

`internal/domain/money/currency.go`:

```go
// Package money implements Money, an immutable value object holding an exact
// amount in minor units (int64, fixed scale of two decimals) and an ISO 4217
// currency. Money never passes through floating point.
package money

import "fmt"

// Currency is an ISO 4217 alphabetic code whose minor unit exponent is 2.
// The zero value is not a valid currency.
type Currency struct {
	code string
}

// Currencies used by the main scenarios and the currency mismatch tests.
var (
	BRL = Currency{code: "BRL"}
	USD = Currency{code: "USD"}
)

// ParseCurrency validates code against the ISO 4217 currencies with exponent 2.
// Codes are case sensitive and must be upper case.
func ParseCurrency(code string) (Currency, error) {
	if _, ok := iso4217Exponent2[code]; !ok {
		return Currency{}, fmt.Errorf("%w: %q", ErrUnsupportedCurrency, code)
	}
	return Currency{code: code}, nil
}

// Code returns the ISO 4217 alphabetic code.
func (c Currency) Code() string { return c.code }

// String implements fmt.Stringer.
func (c Currency) String() string { return c.code }

// IsZero reports whether c is the uninitialized Currency.
func (c Currency) IsZero() bool { return c.code == "" }
```

`internal/domain/money/iso4217.go`:

```go
package money

// iso4217Exponent2 lists the active ISO 4217 (List One) currencies whose minor
// unit exponent is 2. Currencies with exponent 0, 3 or 4 and the "N.A."
// codes (metals, funds, testing) are excluded because Money has a fixed scale
// of two decimals.
var iso4217Exponent2 = map[string]struct{}{
	"AED": {}, "AFN": {}, "ALL": {}, "AMD": {}, "AOA": {}, "ARS": {}, "AUD": {}, "AWG": {},
	"AZN": {}, "BAM": {}, "BBD": {}, "BDT": {}, "BMD": {}, "BND": {}, "BOB": {}, "BOV": {},
	"BRL": {}, "BSD": {}, "BTN": {}, "BWP": {}, "BYN": {}, "BZD": {}, "CAD": {}, "CDF": {},
	"CHE": {}, "CHF": {}, "CHW": {}, "CNY": {}, "COP": {}, "COU": {}, "CRC": {}, "CUP": {},
	"CVE": {}, "CZK": {}, "DKK": {}, "DOP": {}, "DZD": {}, "EGP": {}, "ERN": {}, "ETB": {},
	"EUR": {}, "FJD": {}, "FKP": {}, "GBP": {}, "GEL": {}, "GHS": {}, "GIP": {}, "GMD": {},
	"GTQ": {}, "GYD": {}, "HKD": {}, "HNL": {}, "HTG": {}, "HUF": {}, "IDR": {}, "ILS": {},
	"INR": {}, "IRR": {}, "JMD": {}, "KES": {}, "KGS": {}, "KHR": {}, "KPW": {}, "KYD": {},
	"KZT": {}, "LAK": {}, "LBP": {}, "LKR": {}, "LRD": {}, "LSL": {}, "MAD": {}, "MDL": {},
	"MGA": {}, "MKD": {}, "MMK": {}, "MNT": {}, "MOP": {}, "MRU": {}, "MUR": {}, "MVR": {},
	"MWK": {}, "MXN": {}, "MXV": {}, "MYR": {}, "MZN": {}, "NAD": {}, "NGN": {}, "NIO": {},
	"NOK": {}, "NPR": {}, "NZD": {}, "PAB": {}, "PEN": {}, "PGK": {}, "PHP": {}, "PKR": {},
	"PLN": {}, "QAR": {}, "RON": {}, "RSD": {}, "RUB": {}, "SAR": {}, "SBD": {}, "SCR": {},
	"SDG": {}, "SEK": {}, "SGD": {}, "SHP": {}, "SLE": {}, "SOS": {}, "SRD": {}, "SSP": {},
	"STN": {}, "SVC": {}, "SYP": {}, "SZL": {}, "THB": {}, "TJS": {}, "TMT": {}, "TOP": {},
	"TRY": {}, "TTD": {}, "TWD": {}, "TZS": {}, "UAH": {}, "USD": {}, "USN": {}, "UYU": {},
	"UZS": {}, "VED": {}, "VES": {}, "WST": {}, "XCD": {}, "XCG": {}, "YER": {}, "ZAR": {},
	"ZMW": {}, "ZWG": {},
}
```

- [ ] **Step 5: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/money/...`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: init go module, move challenge to docs; feat(money): ISO 4217 currency" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Money — parsing, formatação e JSON

**Files:**

- Create: `internal/domain/money/money.go`
- Test: `internal/domain/money/money_test.go`

**Interfaces:**

- Consumes: `Currency`, `ParseCurrency` e as sentinelas da Task 1.
- Produces: `money.Money`; `Parse(amount, currency string) (Money, error)`; `FromMinor(minor int64, c Currency) (Money, error)`; `Zero(c Currency) (Money, error)`; métodos `MinorUnits() int64`, `Currency() Currency`, `IsValid()`, `IsZero()`, `IsPositive()`, `IsNegative() bool`, `Amount() string`, `String() string`, `MarshalJSON`, `UnmarshalJSON`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/domain/money/money_test.go`:

```go
//go:build !integration && !e2e

package money

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParseValid(t *testing.T) {
	tests := []struct {
		amount    string
		wantMinor int64
	}{
		{"0.00", 0},
		{"0.01", 1},
		{"25.00", 2500},
		{"1000.00", 100000},
		{"92233720368547758.07", math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.amount, func(t *testing.T) {
			m, err := Parse(tt.amount, "BRL")
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.amount, err)
			}
			if m.MinorUnits() != tt.wantMinor {
				t.Fatalf("MinorUnits() = %d, want %d", m.MinorUnits(), tt.wantMinor)
			}
			if m.Currency() != BRL {
				t.Fatalf("Currency() = %v, want BRL", m.Currency())
			}
			if m.Amount() != tt.amount {
				t.Fatalf("Amount() = %q, want %q (round trip)", m.Amount(), tt.amount)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name, amount, currency string
		want                   error
	}{
		{"empty", "", "BRL", ErrInvalidAmount},
		{"no decimals", "25", "BRL", ErrInvalidAmount},
		{"one decimal", "25.0", "BRL", ErrInvalidAmount},
		{"excess scale", "25.001", "BRL", ErrInvalidAmount},
		{"leading zero", "025.00", "BRL", ErrInvalidAmount},
		{"plus sign", "+25.00", "BRL", ErrInvalidAmount},
		{"negative", "-1.00", "BRL", ErrInvalidAmount},
		{"negative zero", "-0.00", "BRL", ErrInvalidAmount},
		{"plus zero", "+0.00", "BRL", ErrInvalidAmount},
		{"scientific", "1e3", "BRL", ErrInvalidAmount},
		{"scientific decimal", "1.00e2", "BRL", ErrInvalidAmount},
		{"NaN", "NaN", "BRL", ErrInvalidAmount},
		{"Infinity", "Infinity", "BRL", ErrInvalidAmount},
		{"spaces", " 25.00", "BRL", ErrInvalidAmount},
		{"comma", "25,00", "BRL", ErrInvalidAmount},
		{"missing integer", ".50", "BRL", ErrInvalidAmount},
		{"overflow by one cent", "92233720368547758.08", "BRL", ErrOverflow},
		{"overflow integer part", "99999999999999999999.00", "BRL", ErrOverflow},
		{"bad currency", "25.00", "XXX", ErrUnsupportedCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.amount, tt.currency)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Parse(%q, %q) error = %v, want %v", tt.amount, tt.currency, err, tt.want)
			}
		})
	}
}

func TestFromMinorAndFormatting(t *testing.T) {
	tests := []struct {
		minor int64
		want  string
	}{
		{0, "0.00"},
		{-50, "-0.50"},
		{-2500, "-25.00"},
		{123456, "1234.56"},
		{math.MinInt64, "-92233720368547758.08"},
	}
	for _, tt := range tests {
		m, err := FromMinor(tt.minor, BRL)
		if err != nil {
			t.Fatalf("FromMinor(%d) unexpected error: %v", tt.minor, err)
		}
		if got := m.Amount(); got != tt.want {
			t.Errorf("Amount() = %q, want %q", got, tt.want)
		}
	}
	if _, err := FromMinor(1, Currency{}); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("FromMinor with zero currency error = %v, want ErrUninitialized", err)
	}
}

func TestZeroValueIsInvalid(t *testing.T) {
	var m Money
	if m.IsValid() || m.IsZero() || m.IsPositive() || m.IsNegative() {
		t.Fatal("zero Money must be invalid and report no sign")
	}
	if _, err := json.Marshal(m); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Marshal(zero Money) error = %v, want ErrUninitialized", err)
	}
	z, err := Zero(BRL)
	if err != nil || !z.IsValid() || !z.IsZero() {
		t.Fatalf("Zero(BRL) = %v, %v; want valid zero", z, err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	m, err := Parse("25.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"amount":"25.00","currency":"BRL"}` {
		t.Fatalf("Marshal = %s", b)
	}
	var back Money
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != m {
		t.Fatalf("round trip = %v, want %v", back, m)
	}
}

func TestUnmarshalRejectsNonCanonical(t *testing.T) {
	tests := []struct {
		name, input string
	}{
		{"numeric amount", `{"amount":25.00,"currency":"BRL"}`},
		{"missing currency", `{"amount":"25.00"}`},
		{"missing amount", `{"currency":"BRL"}`},
		{"null", `null`},
		{"bad scale", `{"amount":"25.0","currency":"BRL"}`},
		{"negative", `{"amount":"-25.00","currency":"BRL"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Money
			if err := json.Unmarshal([]byte(tt.input), &m); err == nil {
				t.Fatalf("Unmarshal(%s) succeeded with %v, want error", tt.input, m)
			}
		})
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/money/...`
Expected: FAIL de compilação, `undefined: Parse`.

- [ ] **Step 3: Implementar**

`internal/domain/money/money.go`:

```go
package money

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Money is an immutable monetary value: an amount in minor units (cents) and
// its currency. The scale is fixed at two decimals, so the representable range
// is -92233720368547758.08 to 92233720368547758.07. The zero value is
// uninitialized: it is not valid and every operation on it fails.
type Money struct {
	minor    int64
	currency Currency
}

// externalAmount is the only accepted external form: no sign, no leading
// zeros, exactly two decimals.
var externalAmount = regexp.MustCompile(`^(0|[1-9][0-9]*)\.[0-9]{2}$`)

// Parse builds Money from an external decimal string such as "25.00". Only
// the canonical form is accepted, so no normalization happens before hashing.
// Negative values, scientific notation, NaN, Infinity and any other scale are
// rejected; nothing is rounded.
func Parse(amount, currency string) (Money, error) {
	cur, err := ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	if !externalAmount.MatchString(amount) {
		return Money{}, fmt.Errorf("%w: %q must have the form 123.45", ErrInvalidAmount, amount)
	}
	dot := len(amount) - 3
	units, err := strconv.ParseInt(amount[:dot], 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q", ErrOverflow, amount)
	}
	cents, err := strconv.ParseInt(amount[dot+1:], 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}
	if units > (math.MaxInt64-cents)/100 {
		return Money{}, fmt.Errorf("%w: %q", ErrOverflow, amount)
	}
	return Money{minor: units*100 + cents, currency: cur}, nil
}

// FromMinor builds Money from minor units, for example when rehydrating from
// storage. Negative values are allowed for internal calculations.
func FromMinor(minor int64, c Currency) (Money, error) {
	if c.IsZero() {
		return Money{}, ErrUninitialized
	}
	return Money{minor: minor, currency: c}, nil
}

// Zero returns zero in currency c.
func Zero(c Currency) (Money, error) { return FromMinor(0, c) }

// MinorUnits returns the amount in minor units (cents).
func (m Money) MinorUnits() int64 { return m.minor }

// Currency returns the currency.
func (m Money) Currency() Currency { return m.currency }

// IsValid reports whether m was initialized with a currency.
func (m Money) IsValid() bool { return !m.currency.IsZero() }

// IsZero reports whether m is a valid zero amount.
func (m Money) IsZero() bool { return m.IsValid() && m.minor == 0 }

// IsPositive reports whether m is a valid amount greater than zero.
func (m Money) IsPositive() bool { return m.IsValid() && m.minor > 0 }

// IsNegative reports whether m is a valid amount lower than zero.
func (m Money) IsNegative() bool { return m.IsValid() && m.minor < 0 }

// Amount formats the amount with two decimals, e.g. "25.00" or "-0.50".
func (m Money) Amount() string {
	u := uint64(m.minor)
	sign := ""
	if m.minor < 0 {
		sign = "-"
		u = -u // two's complement magnitude; correct for math.MinInt64
	}
	return fmt.Sprintf("%s%d.%02d", sign, u/100, u%100)
}

// String implements fmt.Stringer, e.g. "25.00 BRL".
func (m Money) String() string {
	if !m.IsValid() {
		return "<invalid money>"
	}
	return m.Amount() + " " + m.currency.code
}

type jsonMoney struct {
	Amount   *string `json:"amount"`
	Currency *string `json:"currency"`
}

// MarshalJSON renders {"amount":"25.00","currency":"BRL"}.
func (m Money) MarshalJSON() ([]byte, error) {
	if !m.IsValid() {
		return nil, ErrUninitialized
	}
	amount, currency := m.Amount(), m.currency.code
	return json.Marshal(jsonMoney{Amount: &amount, Currency: &currency})
}

// UnmarshalJSON accepts {"amount":"25.00","currency":"BRL"} with the strict
// rules of Parse. JSON numbers are rejected so no float is ever involved.
func (m *Money) UnmarshalJSON(b []byte) error {
	var j jsonMoney
	if err := json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	if j.Amount == nil || j.Currency == nil {
		return fmt.Errorf("%w: amount and currency are required", ErrInvalidAmount)
	}
	v, err := Parse(*j.Amount, *j.Currency)
	if err != nil {
		return err
	}
	*m = v
	return nil
}
```

- [ ] **Step 4: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/money/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/money
git commit -m "feat(money): strict decimal parsing, formatting and JSON" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Money — aritmética com overflow

**Files:**

- Create: `internal/domain/money/arith.go`
- Test: `internal/domain/money/arith_test.go`

**Interfaces:**

- Produces: `(Money) Add(o Money) (Money, error)`, `Sub(o Money) (Money, error)`, `Negate() (Money, error)`, `Cmp(o Money) (int, error)`, `Equal(o Money) bool`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/domain/money/arith_test.go`:

```go
//go:build !integration && !e2e

package money

import (
	"errors"
	"math"
	"testing"
)

func mustMinor(t *testing.T, minor int64, c Currency) Money {
	t.Helper()
	m, err := FromMinor(minor, c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAddSub(t *testing.T) {
	a, b := mustMinor(t, 10000, BRL), mustMinor(t, 8000, BRL)
	sum, err := a.Add(b)
	if err != nil || sum.MinorUnits() != 18000 {
		t.Fatalf("Add = %v, %v; want 180.00", sum, err)
	}
	diff, err := b.Sub(a)
	if err != nil || diff.MinorUnits() != -2000 || diff.Amount() != "-20.00" {
		t.Fatalf("Sub = %v, %v; want -20.00", diff, err)
	}
}

func TestOverflow(t *testing.T) {
	maxM, minM := mustMinor(t, math.MaxInt64, BRL), mustMinor(t, math.MinInt64, BRL)
	one, minusOne := mustMinor(t, 1, BRL), mustMinor(t, -1, BRL)
	tests := []struct {
		name string
		op   func() (Money, error)
	}{
		{"max+1", func() (Money, error) { return maxM.Add(one) }},
		{"min+(-1)", func() (Money, error) { return minM.Add(minusOne) }},
		{"min-1", func() (Money, error) { return minM.Sub(one) }},
		{"max-(-1)", func() (Money, error) { return maxM.Sub(minusOne) }},
		{"negate min", func() (Money, error) { return minM.Negate() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.op(); !errors.Is(err, ErrOverflow) {
				t.Fatalf("error = %v, want ErrOverflow", err)
			}
		})
	}
	if got, err := maxM.Sub(maxM); err != nil || !got.IsZero() {
		t.Fatalf("max-max = %v, %v; want 0", got, err)
	}
}

func TestNegate(t *testing.T) {
	n, err := mustMinor(t, 2500, BRL).Negate()
	if err != nil || n.MinorUnits() != -2500 {
		t.Fatalf("Negate = %v, %v", n, err)
	}
	if _, err := (Money{}).Negate(); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Negate(zero Money) error = %v, want ErrUninitialized", err)
	}
}

func TestCurrencyMismatch(t *testing.T) {
	brl, usd := mustMinor(t, 100, BRL), mustMinor(t, 100, USD)
	if _, err := brl.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Add error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := brl.Sub(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Sub error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := brl.Cmp(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Cmp error = %v, want ErrCurrencyMismatch", err)
	}
	if brl.Equal(usd) {
		t.Fatal("Equal must be false across currencies")
	}
}

func TestUninitializedOperands(t *testing.T) {
	brl := mustMinor(t, 100, BRL)
	if _, err := brl.Add(Money{}); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Add(zero Money) error = %v, want ErrUninitialized", err)
	}
	if _, err := (Money{}).Cmp(brl); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Cmp on zero Money error = %v, want ErrUninitialized", err)
	}
}

func TestCmp(t *testing.T) {
	a, b := mustMinor(t, 100, BRL), mustMinor(t, 200, BRL)
	for _, tt := range []struct {
		x, y Money
		want int
	}{{a, b, -1}, {b, a, 1}, {a, a, 0}} {
		got, err := tt.x.Cmp(tt.y)
		if err != nil || got != tt.want {
			t.Errorf("Cmp(%v, %v) = %d, %v; want %d", tt.x, tt.y, got, err, tt.want)
		}
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/money/...`
Expected: FAIL de compilação, `maxM.Add undefined`.

- [ ] **Step 3: Implementar**

`internal/domain/money/arith.go`:

```go
package money

import (
	"fmt"
	"math"
)

// Add returns m + o. Both operands must share the currency.
func (m Money) Add(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	r := m.minor + o.minor
	if (o.minor > 0 && r < m.minor) || (o.minor < 0 && r > m.minor) {
		return Money{}, fmt.Errorf("%w: %s + %s", ErrOverflow, m, o)
	}
	return Money{minor: r, currency: m.currency}, nil
}

// Sub returns m - o. Both operands must share the currency. The result may be
// negative (differences); non-negativity of balances is enforced by Wallet.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	r := m.minor - o.minor
	if (o.minor > 0 && r > m.minor) || (o.minor < 0 && r < m.minor) {
		return Money{}, fmt.Errorf("%w: %s - %s", ErrOverflow, m, o)
	}
	return Money{minor: r, currency: m.currency}, nil
}

// Negate returns -m.
func (m Money) Negate() (Money, error) {
	if !m.IsValid() {
		return Money{}, ErrUninitialized
	}
	if m.minor == math.MinInt64 {
		return Money{}, fmt.Errorf("%w: -(%s)", ErrOverflow, m)
	}
	return Money{minor: -m.minor, currency: m.currency}, nil
}

// Cmp returns -1, 0 or +1 when m is lower than, equal to or greater than o.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.compatible(o); err != nil {
		return 0, err
	}
	switch {
	case m.minor < o.minor:
		return -1, nil
	case m.minor > o.minor:
		return 1, nil
	default:
		return 0, nil
	}
}

// Equal reports whether m and o have the same amount and currency.
func (m Money) Equal(o Money) bool { return m == o }

func (m Money) compatible(o Money) error {
	if !m.IsValid() || !o.IsValid() {
		return ErrUninitialized
	}
	if m.currency != o.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, o.currency)
	}
	return nil
}
```

- [ ] **Step 4: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/money/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/money
git commit -m "feat(money): overflow-checked arithmetic and comparison" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: LedgerEntry

**Files:**

- Create: `internal/domain/wallet/errors.go`, `internal/domain/wallet/ledger.go`
- Test: `internal/domain/wallet/ledger_test.go`

**Interfaces:**

- Consumes: `money.Money` (Tasks 2–3).
- Produces: `wallet.Direction` (`DirectionDebit`, `DirectionCredit`, `IsValid()`), `wallet.LedgerEntryParams`, `wallet.NewLedgerEntry(p LedgerEntryParams) (LedgerEntry, error)`, getters `ID()`, `WalletID()`, `TransactionID() uuid.UUID`, `Direction() Direction`, `Amount()`, `BalanceBefore()`, `BalanceAfter() money.Money`, `CreatedAt() time.Time`; sentinelas `ErrInvalidWallet`, `ErrInvalidLedgerEntry`, `ErrInsufficientFunds`, `ErrCurrencyMismatch`, `ErrInvalidAmount`.

- [ ] **Step 1: Adicionar a dependência de UUID**

```bash
go get github.com/google/uuid@v1.6.0
```

- [ ] **Step 2: Escrever o teste que falha**

`internal/domain/wallet/ledger_test.go`:

```go
//go:build !integration && !e2e

package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func brl(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.FromMinor(minor, money.BRL)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func validEntryParams(t *testing.T) LedgerEntryParams {
	return LedgerEntryParams{
		ID:            uuid.New(),
		WalletID:      uuid.New(),
		TransactionID: uuid.New(),
		Direction:     DirectionDebit,
		Amount:        brl(t, 2500),
		BalanceBefore: brl(t, 100000),
		BalanceAfter:  brl(t, 97500),
		CreatedAt:     testNow,
	}
}

func TestNewLedgerEntryValid(t *testing.T) {
	p := validEntryParams(t)
	e, err := NewLedgerEntry(p)
	if err != nil {
		t.Fatalf("NewLedgerEntry: %v", err)
	}
	if e.ID() != p.ID || e.WalletID() != p.WalletID || e.TransactionID() != p.TransactionID ||
		e.Direction() != DirectionDebit || !e.Amount().Equal(p.Amount) ||
		!e.BalanceBefore().Equal(p.BalanceBefore) || !e.BalanceAfter().Equal(p.BalanceAfter) ||
		!e.CreatedAt().Equal(testNow) {
		t.Fatalf("entry fields do not match params: %+v", e)
	}

	credit := validEntryParams(t)
	credit.Direction = DirectionCredit
	credit.BalanceAfter = brl(t, 102500)
	if _, err := NewLedgerEntry(credit); err != nil {
		t.Fatalf("credit entry: %v", err)
	}
}

func TestNewLedgerEntryInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *LedgerEntryParams)
	}{
		{"nil id", func(p *LedgerEntryParams) { p.ID = uuid.Nil }},
		{"nil wallet", func(p *LedgerEntryParams) { p.WalletID = uuid.Nil }},
		{"nil transaction", func(p *LedgerEntryParams) { p.TransactionID = uuid.Nil }},
		{"bad direction", func(p *LedgerEntryParams) { p.Direction = "SIDEWAYS" }},
		{"zero amount", func(p *LedgerEntryParams) { p.Amount = brl(t, 0) }},
		{"negative amount", func(p *LedgerEntryParams) { p.Amount = brl(t, -2500) }},
		{"uninitialized amount", func(p *LedgerEntryParams) { p.Amount = money.Money{} }},
		{"wrong after", func(p *LedgerEntryParams) { p.BalanceAfter = brl(t, 97400) }},
		{"credit math on debit", func(p *LedgerEntryParams) { p.BalanceAfter = brl(t, 102500) }},
		{"negative after", func(p *LedgerEntryParams) {
			p.BalanceBefore, p.BalanceAfter = brl(t, 1000), brl(t, -1500)
		}},
		{"currency mismatch", func(p *LedgerEntryParams) {
			usd, _ := money.FromMinor(97500, money.USD)
			p.BalanceAfter = usd
		}},
		{"zero time", func(p *LedgerEntryParams) { p.CreatedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validEntryParams(t)
			tt.mutate(&p)
			if _, err := NewLedgerEntry(p); !errors.Is(err, ErrInvalidLedgerEntry) {
				t.Fatalf("error = %v, want ErrInvalidLedgerEntry", err)
			}
		})
	}
}
```

- [ ] **Step 3: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/wallet/...`
Expected: FAIL de compilação, `undefined: LedgerEntryParams`.

- [ ] **Step 4: Implementar**

`internal/domain/wallet/errors.go`:

```go
package wallet

import "errors"

var (
	// ErrInvalidWallet reports invalid wallet construction or rehydration data.
	ErrInvalidWallet = errors.New("wallet: invalid wallet")
	// ErrInvalidLedgerEntry reports an entry that violates its invariants.
	ErrInvalidLedgerEntry = errors.New("wallet: invalid ledger entry")
	// ErrInsufficientFunds reports a debit larger than the balance.
	ErrInsufficientFunds = errors.New("wallet: insufficient funds")
	// ErrCurrencyMismatch reports a movement in a currency other than the wallet's.
	ErrCurrencyMismatch = errors.New("wallet: currency mismatch")
	// ErrInvalidAmount reports a movement amount that is not positive.
	ErrInvalidAmount = errors.New("wallet: movement amount must be positive")
)
```

`internal/domain/wallet/ledger.go`:

```go
// Package wallet holds the Wallet aggregate root and its append-only ledger.
package wallet

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// Direction is the side of a ledger movement.
type Direction string

// Ledger directions.
const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

// IsValid reports whether d is DEBIT or CREDIT.
func (d Direction) IsValid() bool { return d == DirectionDebit || d == DirectionCredit }

// LedgerEntry is an immutable record of one balance movement. Corrections are
// new entries; an entry is never edited.
type LedgerEntry struct {
	id            uuid.UUID
	walletID      uuid.UUID
	transactionID uuid.UUID
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

// LedgerEntryParams carries the fields of a LedgerEntry.
type LedgerEntryParams struct {
	ID            uuid.UUID
	WalletID      uuid.UUID
	TransactionID uuid.UUID
	Direction     Direction
	Amount        money.Money
	BalanceBefore money.Money
	BalanceAfter  money.Money
	CreatedAt     time.Time
}

// NewLedgerEntry validates p and builds an entry, enforcing
// balanceAfter = balanceBefore ± amount by direction and non-negative
// balances. It serves both creation and rehydration: it has no side effects.
func NewLedgerEntry(p LedgerEntryParams) (LedgerEntry, error) {
	if p.ID == uuid.Nil || p.WalletID == uuid.Nil || p.TransactionID == uuid.Nil {
		return LedgerEntry{}, fmt.Errorf("%w: id, walletId and transactionId are required", ErrInvalidLedgerEntry)
	}
	if !p.Direction.IsValid() {
		return LedgerEntry{}, fmt.Errorf("%w: direction %q", ErrInvalidLedgerEntry, p.Direction)
	}
	if !p.Amount.IsPositive() {
		return LedgerEntry{}, fmt.Errorf("%w: amount must be positive", ErrInvalidLedgerEntry)
	}
	if p.CreatedAt.IsZero() {
		return LedgerEntry{}, fmt.Errorf("%w: createdAt is required", ErrInvalidLedgerEntry)
	}
	if !p.BalanceBefore.IsValid() || !p.BalanceAfter.IsValid() ||
		p.BalanceBefore.IsNegative() || p.BalanceAfter.IsNegative() {
		return LedgerEntry{}, fmt.Errorf("%w: balances must be valid and non-negative", ErrInvalidLedgerEntry)
	}
	var expected money.Money
	var err error
	if p.Direction == DirectionCredit {
		expected, err = p.BalanceBefore.Add(p.Amount)
	} else {
		expected, err = p.BalanceBefore.Sub(p.Amount)
	}
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("%w: %v", ErrInvalidLedgerEntry, err)
	}
	if !expected.Equal(p.BalanceAfter) {
		return LedgerEntry{}, fmt.Errorf("%w: balanceAfter %s, expected %s", ErrInvalidLedgerEntry, p.BalanceAfter, expected)
	}
	return LedgerEntry{
		id:            p.ID,
		walletID:      p.WalletID,
		transactionID: p.TransactionID,
		direction:     p.Direction,
		amount:        p.Amount,
		balanceBefore: p.BalanceBefore,
		balanceAfter:  p.BalanceAfter,
		createdAt:     p.CreatedAt.UTC(),
	}, nil
}

// ID returns the entry id.
func (e LedgerEntry) ID() uuid.UUID { return e.id }

// WalletID returns the wallet the entry belongs to.
func (e LedgerEntry) WalletID() uuid.UUID { return e.walletID }

// TransactionID returns the wager transaction that produced the entry.
func (e LedgerEntry) TransactionID() uuid.UUID { return e.transactionID }

// Direction returns DEBIT or CREDIT.
func (e LedgerEntry) Direction() Direction { return e.direction }

// Amount returns the moved amount (always positive).
func (e LedgerEntry) Amount() money.Money { return e.amount }

// BalanceBefore returns the wallet balance before the movement.
func (e LedgerEntry) BalanceBefore() money.Money { return e.balanceBefore }

// BalanceAfter returns the wallet balance after the movement.
func (e LedgerEntry) BalanceAfter() money.Money { return e.balanceAfter }

// CreatedAt returns the creation instant (UTC).
func (e LedgerEntry) CreatedAt() time.Time { return e.createdAt }
```

- [ ] **Step 5: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/wallet/...`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/domain/wallet
git commit -m "feat(wallet): immutable ledger entry with balance invariant" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Wallet aggregate

**Files:**

- Create: `internal/domain/wallet/wallet.go`
- Test: `internal/domain/wallet/wallet_test.go`

**Interfaces:**

- Consumes: `NewLedgerEntry` e as sentinelas da Task 4.
- Produces: `wallet.Wallet`; `wallet.InitialVersion` (=1); `wallet.OpenParams{ID, PlayerID uuid.UUID; InitialBalance money.Money; OpeningTransactionID, LedgerEntryID uuid.UUID; Now time.Time}`; `wallet.Open(p OpenParams) (*Wallet, *LedgerEntry, error)`; `wallet.RehydrateParams{ID, PlayerID uuid.UUID; Balance money.Money; Version int64; CreatedAt, UpdatedAt time.Time}`; `wallet.Rehydrate(p RehydrateParams) (*Wallet, error)`; `(*Wallet) Debit(transactionID, entryID uuid.UUID, amount money.Money, now time.Time) (LedgerEntry, error)` e `Credit(...)` com a mesma assinatura; getters `ID()`, `PlayerID() uuid.UUID`, `Currency() money.Currency`, `Balance() money.Money`, `Version() int64`, `CreatedAt()`, `UpdatedAt() time.Time`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/domain/wallet/wallet_test.go`:

```go
//go:build !integration && !e2e

package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

func openWallet(t *testing.T, balanceMinor int64) *Wallet {
	t.Helper()
	w, _, err := Open(OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, balanceMinor),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: testNow,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return w
}

func TestOpenWithPositiveBalance(t *testing.T) {
	p := OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 100000),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: testNow,
	}
	w, entry, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if w.Version() != InitialVersion || w.Version() != 1 {
		t.Fatalf("Version() = %d, want 1 (opening credit must not bump the version)", w.Version())
	}
	if !w.Balance().Equal(p.InitialBalance) || w.Currency() != money.BRL {
		t.Fatalf("Balance() = %v", w.Balance())
	}
	if entry == nil {
		t.Fatal("positive opening must produce a ledger entry")
	}
	if entry.Direction() != DirectionCredit || !entry.BalanceBefore().IsZero() ||
		!entry.BalanceAfter().Equal(p.InitialBalance) || entry.TransactionID() != p.OpeningTransactionID ||
		entry.ID() != p.LedgerEntryID || entry.WalletID() != p.ID {
		t.Fatalf("unexpected opening entry %+v", entry)
	}
}

func TestOpenWithZeroBalance(t *testing.T) {
	w, entry, err := Open(OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 0), Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if entry != nil {
		t.Fatal("zero opening must not produce a ledger entry")
	}
	if w.Version() != 1 || !w.Balance().IsZero() {
		t.Fatalf("wallet = %+v", w)
	}
}

func TestOpenInvalid(t *testing.T) {
	tests := []struct {
		name string
		p    OpenParams
	}{
		{"nil id", OpenParams{PlayerID: uuid.New(), InitialBalance: brl(t, 0), Now: testNow}},
		{"nil player", OpenParams{ID: uuid.New(), InitialBalance: brl(t, 0), Now: testNow}},
		{"negative", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, -1), Now: testNow}},
		{"uninitialized", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), Now: testNow}},
		{"zero time", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 0)}},
		{"positive without entry ids", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 100), Now: testNow}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Open(tt.p); err == nil {
				t.Fatal("Open succeeded, want error")
			}
		})
	}
}

func TestRehydrate(t *testing.T) {
	valid := RehydrateParams{
		ID: uuid.New(), PlayerID: uuid.New(), Balance: brl(t, 2000), Version: 7,
		CreatedAt: testNow, UpdatedAt: testNow.Add(time.Hour),
	}
	w, err := Rehydrate(valid)
	if err != nil {
		t.Fatal(err)
	}
	if w.Version() != 7 || !w.Balance().Equal(valid.Balance) || !w.UpdatedAt().Equal(valid.UpdatedAt) {
		t.Fatalf("rehydrated wallet = %+v", w)
	}

	tests := []struct {
		name   string
		mutate func(p *RehydrateParams)
	}{
		{"version 0", func(p *RehydrateParams) { p.Version = 0 }},
		{"negative balance", func(p *RehydrateParams) { p.Balance = brl(t, -1) }},
		{"uninitialized balance", func(p *RehydrateParams) { p.Balance = money.Money{} }},
		{"nil id", func(p *RehydrateParams) { p.ID = uuid.Nil }},
		{"nil player", func(p *RehydrateParams) { p.PlayerID = uuid.Nil }},
		{"zero createdAt", func(p *RehydrateParams) { p.CreatedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid
			tt.mutate(&p)
			if _, err := Rehydrate(p); !errors.Is(err, ErrInvalidWallet) {
				t.Fatalf("error = %v, want ErrInvalidWallet", err)
			}
		})
	}
}

func TestDebitAndCredit(t *testing.T) {
	w := openWallet(t, 10000)
	later := testNow.Add(time.Minute)

	entry, err := w.Debit(uuid.New(), uuid.New(), brl(t, 8000), later)
	if err != nil {
		t.Fatal(err)
	}
	if w.Balance().MinorUnits() != 2000 || w.Version() != 2 || !w.UpdatedAt().Equal(later) {
		t.Fatalf("after debit: balance %v version %d", w.Balance(), w.Version())
	}
	if entry.Direction() != DirectionDebit || entry.BalanceBefore().MinorUnits() != 10000 ||
		entry.BalanceAfter().MinorUnits() != 2000 {
		t.Fatalf("debit entry %+v", entry)
	}

	if _, err := w.Credit(uuid.New(), uuid.New(), brl(t, 500), later); err != nil {
		t.Fatal(err)
	}
	if w.Balance().MinorUnits() != 2500 || w.Version() != 3 {
		t.Fatalf("after credit: balance %v version %d", w.Balance(), w.Version())
	}
}

func TestDebitExactBalance(t *testing.T) {
	w := openWallet(t, 10000)
	if _, err := w.Debit(uuid.New(), uuid.New(), brl(t, 10000), testNow); err != nil {
		t.Fatal(err)
	}
	if !w.Balance().IsZero() {
		t.Fatalf("balance = %v, want 0.00", w.Balance())
	}
}

func TestMovementRejectionsLeaveWalletUntouched(t *testing.T) {
	usd, _ := money.FromMinor(100, money.USD)
	maxBRL, _ := money.FromMinor(1<<62, money.BRL)
	tests := []struct {
		name    string
		balance int64
		op      func(w *Wallet) error
		want    error
	}{
		{"insufficient", 10000, func(w *Wallet) error {
			_, err := w.Debit(uuid.New(), uuid.New(), brl(t, 10001), testNow)
			return err
		}, ErrInsufficientFunds},
		{"currency mismatch", 10000, func(w *Wallet) error {
			_, err := w.Credit(uuid.New(), uuid.New(), usd, testNow)
			return err
		}, ErrCurrencyMismatch},
		{"zero amount", 10000, func(w *Wallet) error {
			_, err := w.Debit(uuid.New(), uuid.New(), brl(t, 0), testNow)
			return err
		}, ErrInvalidAmount},
		{"credit overflow", 1 << 62, func(w *Wallet) error {
			_, err := w.Credit(uuid.New(), uuid.New(), maxBRL, testNow)
			return err
		}, money.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := openWallet(t, tt.balance)
			before, version := w.Balance(), w.Version()
			if err := tt.op(w); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if !w.Balance().Equal(before) || w.Version() != version {
				t.Fatal("rejected movement must not change the wallet")
			}
		})
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/wallet/...`
Expected: FAIL de compilação, `undefined: Open`.

- [ ] **Step 3: Implementar**

`internal/domain/wallet/wallet.go`:

```go
package wallet

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// InitialVersion is the version of a newly opened wallet. The version only
// increases when the balance changes.
const InitialVersion int64 = 1

// Wallet is the financial aggregate root. One wallet exists per
// (playerId, currency). Its balance is never negative.
type Wallet struct {
	id        uuid.UUID
	playerID  uuid.UUID
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

// OpenParams carries the data needed to open a wallet.
type OpenParams struct {
	ID             uuid.UUID
	PlayerID       uuid.UUID
	InitialBalance money.Money
	// OpeningTransactionID and LedgerEntryID identify the opening credit and
	// are required only when InitialBalance is positive.
	OpeningTransactionID uuid.UUID
	LedgerEntryID        uuid.UUID
	Now                  time.Time
}

// Open creates a wallet at version 1. A positive initial balance also returns
// the opening credit entry (0 → initial) without bumping the version; a zero
// initial balance returns no entry.
func Open(p OpenParams) (*Wallet, *LedgerEntry, error) {
	if p.ID == uuid.Nil || p.PlayerID == uuid.Nil {
		return nil, nil, fmt.Errorf("%w: id and playerId are required", ErrInvalidWallet)
	}
	if !p.InitialBalance.IsValid() || p.InitialBalance.IsNegative() {
		return nil, nil, fmt.Errorf("%w: initial balance must be valid and non-negative", ErrInvalidWallet)
	}
	if p.Now.IsZero() {
		return nil, nil, fmt.Errorf("%w: now is required", ErrInvalidWallet)
	}
	now := p.Now.UTC()
	w := &Wallet{
		id:        p.ID,
		playerID:  p.PlayerID,
		balance:   p.InitialBalance,
		version:   InitialVersion,
		createdAt: now,
		updatedAt: now,
	}
	if p.InitialBalance.IsZero() {
		return w, nil, nil
	}
	zero, err := money.Zero(p.InitialBalance.Currency())
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidWallet, err)
	}
	entry, err := NewLedgerEntry(LedgerEntryParams{
		ID:            p.LedgerEntryID,
		WalletID:      p.ID,
		TransactionID: p.OpeningTransactionID,
		Direction:     DirectionCredit,
		Amount:        p.InitialBalance,
		BalanceBefore: zero,
		BalanceAfter:  p.InitialBalance,
		CreatedAt:     now,
	})
	if err != nil {
		return nil, nil, err
	}
	return w, &entry, nil
}

// RehydrateParams carries a stored wallet.
type RehydrateParams struct {
	ID        uuid.UUID
	PlayerID  uuid.UUID
	Balance   money.Money
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Rehydrate rebuilds a stored wallet without applying any movement.
func Rehydrate(p RehydrateParams) (*Wallet, error) {
	if p.ID == uuid.Nil || p.PlayerID == uuid.Nil {
		return nil, fmt.Errorf("%w: id and playerId are required", ErrInvalidWallet)
	}
	if !p.Balance.IsValid() || p.Balance.IsNegative() {
		return nil, fmt.Errorf("%w: balance must be valid and non-negative", ErrInvalidWallet)
	}
	if p.Version < InitialVersion {
		return nil, fmt.Errorf("%w: version %d < %d", ErrInvalidWallet, p.Version, InitialVersion)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("%w: timestamps are required", ErrInvalidWallet)
	}
	return &Wallet{
		id:        p.ID,
		playerID:  p.PlayerID,
		balance:   p.Balance,
		version:   p.Version,
		createdAt: p.CreatedAt.UTC(),
		updatedAt: p.UpdatedAt.UTC(),
	}, nil
}

// Debit subtracts amount, keeping the balance non-negative, bumps the version
// and returns the ledger entry to be committed with the new balance.
func (w *Wallet) Debit(transactionID, entryID uuid.UUID, amount money.Money, now time.Time) (LedgerEntry, error) {
	return w.move(DirectionDebit, transactionID, entryID, amount, now)
}

// Credit adds amount, bumps the version and returns the ledger entry.
func (w *Wallet) Credit(transactionID, entryID uuid.UUID, amount money.Money, now time.Time) (LedgerEntry, error) {
	return w.move(DirectionCredit, transactionID, entryID, amount, now)
}

func (w *Wallet) move(dir Direction, transactionID, entryID uuid.UUID, amount money.Money, now time.Time) (LedgerEntry, error) {
	if !amount.IsPositive() {
		return LedgerEntry{}, ErrInvalidAmount
	}
	if amount.Currency() != w.Currency() {
		return LedgerEntry{}, fmt.Errorf("%w: wallet %s, movement %s", ErrCurrencyMismatch, w.Currency(), amount.Currency())
	}
	var after money.Money
	var err error
	if dir == DirectionDebit {
		cmp, cmpErr := w.balance.Cmp(amount)
		if cmpErr != nil {
			return LedgerEntry{}, cmpErr
		}
		if cmp < 0 {
			return LedgerEntry{}, fmt.Errorf("%w: balance %s, debit %s", ErrInsufficientFunds, w.balance, amount)
		}
		after, err = w.balance.Sub(amount)
	} else {
		after, err = w.balance.Add(amount)
	}
	if err != nil {
		return LedgerEntry{}, err
	}
	entry, err := NewLedgerEntry(LedgerEntryParams{
		ID:            entryID,
		WalletID:      w.id,
		TransactionID: transactionID,
		Direction:     dir,
		Amount:        amount,
		BalanceBefore: w.balance,
		BalanceAfter:  after,
		CreatedAt:     now,
	})
	if err != nil {
		return LedgerEntry{}, err
	}
	w.balance = after
	w.version++
	w.updatedAt = now.UTC()
	return entry, nil
}

// ID returns the wallet id.
func (w *Wallet) ID() uuid.UUID { return w.id }

// PlayerID returns the owning player.
func (w *Wallet) PlayerID() uuid.UUID { return w.playerID }

// Currency returns the wallet currency.
func (w *Wallet) Currency() money.Currency { return w.balance.Currency() }

// Balance returns the current balance.
func (w *Wallet) Balance() money.Money { return w.balance }

// Version returns the optimistic version, incremented on every balance change.
func (w *Wallet) Version() int64 { return w.version }

// CreatedAt returns the creation instant (UTC).
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }

// UpdatedAt returns the last change instant (UTC).
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }
```

- [ ] **Step 4: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/wallet/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/wallet
git commit -m "feat(wallet): wallet aggregate with open, rehydrate, debit and credit" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Eventos de integração

**Files:**

- Create: `internal/domain/events/events.go`, `internal/domain/events/types.go`
- Test: `internal/domain/events/events_test.go`

**Interfaces:**

- Consumes: `money.Money`.
- Produces: `events.Data` (interface com `EventType() string`, `EventVersion() int`, `AggregateID() uuid.UUID`); `events.Envelope`; `events.NewEnvelope(eventID uuid.UUID, data Data, correlationID, causationID string, occurredAt time.Time) (Envelope, error)`; `events.FormatTime(t time.Time) string`; `events.ErrInvalidEnvelope`; os tipos `WagerTransactionProcessed`, `WagerTransactionRejected`, `WalletBalanceChanged` e `WagerTransactionPendingReference` (campos abaixo); constantes `TypeWagerTransactionProcessed`, `TypeWagerTransactionRejected`, `TypeWalletBalanceChanged` e `TypeWagerTransactionPendingReference`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/domain/events/events_test.go`:

```go
//go:build !integration && !e2e

package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

func brl(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEnvelopeJSONContract(t *testing.T) {
	walletID := uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef37")
	data := WalletBalanceChanged{
		WalletID:      walletID,
		TransactionID: uuid.MustParse("0192f298-345e-7e38-af88-e43f851a819d"),
		Direction:     "DEBIT",
		Money:         brl(t, "25.00"),
		BalanceBefore: brl(t, "1000.00"),
		BalanceAfter:  brl(t, "975.00"),
		WalletVersion: 2,
	}
	occurred := time.Date(2026, 9, 8, 9, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	env, err := NewEnvelope(uuid.MustParse("0192f2a0-0000-7000-8000-000000000001"), data, "corr-1", "msg-123", occurred)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"eventId":"0192f2a0-0000-7000-8000-000000000001","eventType":"WalletBalanceChanged",` +
		`"aggregateId":"0192f291-27dd-7d3f-8071-5f8685deef37","correlationId":"corr-1","causationId":"msg-123",` +
		`"occurredAt":"2026-09-08T12:00:00.000Z","version":1,"data":{"walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",` +
		`"transactionId":"0192f298-345e-7e38-af88-e43f851a819d","direction":"DEBIT",` +
		`"money":{"amount":"25.00","currency":"BRL"},"balanceBefore":{"amount":"1000.00","currency":"BRL"},` +
		`"balanceAfter":{"amount":"975.00","currency":"BRL"},"walletVersion":2}}`
	if string(b) != want {
		t.Fatalf("envelope JSON\n got: %s\nwant: %s", b, want)
	}
}

func TestEnvelopeOmitsEmptyCausation(t *testing.T) {
	data := WagerTransactionRejected{
		TransactionID: uuid.New(), WalletID: uuid.New(), ProviderID: "provider-a",
		ExternalTransactionID: "tx-1", Kind: "BET", Money: brl(t, "80.00"), FailureCode: "INSUFFICIENT_FUNDS",
	}
	env, err := NewEnvelope(uuid.New(), data, "corr", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(env)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["causationId"]; ok {
		t.Fatal("empty causationId must be omitted")
	}
	if m["eventType"] != TypeWagerTransactionRejected || m["aggregateId"] != data.WalletID.String() {
		t.Fatalf("envelope = %v", m)
	}
}

func TestEventTypesAndVersions(t *testing.T) {
	tests := []struct {
		data Data
		want string
	}{
		{WagerTransactionProcessed{}, "WagerTransactionProcessed"},
		{WagerTransactionRejected{}, "WagerTransactionRejected"},
		{WalletBalanceChanged{}, "WalletBalanceChanged"},
		{WagerTransactionPendingReference{}, "WagerTransactionPendingReference"},
	}
	for _, tt := range tests {
		if tt.data.EventType() != tt.want || tt.data.EventVersion() != 1 {
			t.Errorf("%T: type %q version %d", tt.data, tt.data.EventType(), tt.data.EventVersion())
		}
	}
}

func TestNewEnvelopeValidation(t *testing.T) {
	data := WalletBalanceChanged{WalletID: uuid.New()}
	now := time.Now()
	cases := map[string]func() (Envelope, error){
		"nil event id": func() (Envelope, error) { return NewEnvelope(uuid.Nil, data, "c", "", now) },
		"nil data":     func() (Envelope, error) { return NewEnvelope(uuid.New(), nil, "c", "", now) },
		"no correlation": func() (Envelope, error) {
			return NewEnvelope(uuid.New(), data, "", "", now)
		},
		"zero time": func() (Envelope, error) { return NewEnvelope(uuid.New(), data, "c", "", time.Time{}) },
		"nil aggregate": func() (Envelope, error) {
			return NewEnvelope(uuid.New(), WalletBalanceChanged{}, "c", "", now)
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := fn(); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("error = %v, want ErrInvalidEnvelope", err)
			}
		})
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/events/...`
Expected: FAIL de compilação, `undefined: WalletBalanceChanged`.

- [ ] **Step 3: Implementar**

`internal/domain/events/events.go`:

```go
// Package events defines the integration events written to the outbox and
// published after commit. Each concrete type fixes its own event type and
// schema version; the envelope copies them at construction.
package events

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidEnvelope reports missing envelope metadata.
var ErrInvalidEnvelope = errors.New("events: invalid envelope")

// Data is the typed payload of an integration event.
type Data interface {
	EventType() string
	EventVersion() int
	AggregateID() uuid.UUID
}

const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// FormatTime renders t as RFC 3339 in UTC with millisecond precision.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// Envelope is the immutable outer structure of every published event.
type Envelope struct {
	EventID       uuid.UUID `json:"eventId"`
	EventType     string    `json:"eventType"`
	AggregateID   uuid.UUID `json:"aggregateId"`
	CorrelationID string    `json:"correlationId"`
	CausationID   string    `json:"causationId,omitempty"`
	OccurredAt    string    `json:"occurredAt"`
	Version       int       `json:"version"`
	Data          Data      `json:"data"`
}

// NewEnvelope wraps data. Event type, version and aggregate come from data.
func NewEnvelope(eventID uuid.UUID, data Data, correlationID, causationID string, occurredAt time.Time) (Envelope, error) {
	switch {
	case eventID == uuid.Nil:
		return Envelope{}, fmt.Errorf("%w: eventId is required", ErrInvalidEnvelope)
	case data == nil:
		return Envelope{}, fmt.Errorf("%w: data is required", ErrInvalidEnvelope)
	case data.AggregateID() == uuid.Nil:
		return Envelope{}, fmt.Errorf("%w: aggregateId is required", ErrInvalidEnvelope)
	case correlationID == "":
		return Envelope{}, fmt.Errorf("%w: correlationId is required", ErrInvalidEnvelope)
	case occurredAt.IsZero():
		return Envelope{}, fmt.Errorf("%w: occurredAt is required", ErrInvalidEnvelope)
	}
	return Envelope{
		EventID:       eventID,
		EventType:     data.EventType(),
		AggregateID:   data.AggregateID(),
		CorrelationID: correlationID,
		CausationID:   causationID,
		OccurredAt:    FormatTime(occurredAt),
		Version:       data.EventVersion(),
		Data:          data,
	}, nil
}
```

`internal/domain/events/types.go`:

```go
package events

import (
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// Event type names.
const (
	TypeWagerTransactionProcessed        = "WagerTransactionProcessed"
	TypeWagerTransactionRejected         = "WagerTransactionRejected"
	TypeWalletBalanceChanged             = "WalletBalanceChanged"
	TypeWagerTransactionPendingReference = "WagerTransactionPendingReference"
)

// WagerTransactionProcessed is emitted when an operation succeeds, including
// LOSS and the internal OPENING. External metadata is omitted for OPENING.
type WagerTransactionProcessed struct {
	TransactionID          uuid.UUID   `json:"transactionId"`
	WalletID               uuid.UUID   `json:"walletId"`
	PlayerID               uuid.UUID   `json:"playerId"`
	Origin                 string      `json:"origin"`
	Kind                   string      `json:"kind"`
	Money                  money.Money `json:"money"`
	ProviderID             string      `json:"providerId,omitempty"`
	ExternalTransactionID  string      `json:"externalTransactionId,omitempty"`
	RoundID                string      `json:"roundId,omitempty"`
	GameID                 string      `json:"gameId,omitempty"`
	ReferenceTransactionID *uuid.UUID  `json:"referenceTransactionId,omitempty"`
	BalanceAfter           money.Money `json:"balanceAfter"`
}

// EventType implements Data.
func (WagerTransactionProcessed) EventType() string { return TypeWagerTransactionProcessed }

// EventVersion implements Data.
func (WagerTransactionProcessed) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WagerTransactionProcessed) AggregateID() uuid.UUID { return e.WalletID }

// WagerTransactionRejected is emitted on a definitive business rejection.
type WagerTransactionRejected struct {
	TransactionID         uuid.UUID   `json:"transactionId"`
	WalletID              uuid.UUID   `json:"walletId"`
	ProviderID            string      `json:"providerId"`
	ExternalTransactionID string      `json:"externalTransactionId"`
	Kind                  string      `json:"kind"`
	Money                 money.Money `json:"money"`
	FailureCode           string      `json:"failureCode"`
}

// EventType implements Data.
func (WagerTransactionRejected) EventType() string { return TypeWagerTransactionRejected }

// EventVersion implements Data.
func (WagerTransactionRejected) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WagerTransactionRejected) AggregateID() uuid.UUID { return e.WalletID }

// WalletBalanceChanged is emitted for every effective balance change.
type WalletBalanceChanged struct {
	WalletID      uuid.UUID   `json:"walletId"`
	TransactionID uuid.UUID   `json:"transactionId"`
	Direction     string      `json:"direction"`
	Money         money.Money `json:"money"`
	BalanceBefore money.Money `json:"balanceBefore"`
	BalanceAfter  money.Money `json:"balanceAfter"`
	WalletVersion int64       `json:"walletVersion"`
}

// EventType implements Data.
func (WalletBalanceChanged) EventType() string { return TypeWalletBalanceChanged }

// EventVersion implements Data.
func (WalletBalanceChanged) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WalletBalanceChanged) AggregateID() uuid.UUID { return e.WalletID }

// WagerTransactionPendingReference is emitted when an operation starts waiting
// for a reference that has not arrived yet.
type WagerTransactionPendingReference struct {
	TransactionID                  uuid.UUID `json:"transactionId"`
	WalletID                       uuid.UUID `json:"walletId"`
	ProviderID                     string    `json:"providerId"`
	ExternalTransactionID          string    `json:"externalTransactionId"`
	Kind                           string    `json:"kind"`
	ReferenceExternalTransactionID string    `json:"referenceExternalTransactionId"`
	Attempts                       int       `json:"attempts"`
	NextAttemptAt                  string    `json:"nextAttemptAt"`
}

// EventType implements Data.
func (WagerTransactionPendingReference) EventType() string {
	return TypeWagerTransactionPendingReference
}

// EventVersion implements Data.
func (WagerTransactionPendingReference) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WagerTransactionPendingReference) AggregateID() uuid.UUID { return e.WalletID }
```

- [ ] **Step 4: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/events/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/events
git commit -m "feat(events): typed integration events and envelope" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Failure codes, Kind, Status e máquina de estados

**Files:**

- Create: `internal/domain/wagering/failure.go`, `internal/domain/wagering/kind.go`, `internal/domain/wagering/errors.go`
- Test: `internal/domain/wagering/failure_test.go`, `internal/domain/wagering/kind_test.go`

**Interfaces:**

- Produces:
  - `FailureCode` (constantes abaixo), `Category` (`CategoryCorrectable`, `CategoryDefinitive`), `(FailureCode) Category() Category`, `(FailureCode) IsBusinessRejection() bool`.
  - `Violation{Code FailureCode; Field, Reason string}` e `InputError{Violations []Violation}`, com `Error()` e `Code() FailureCode`.
  - `Kind` (`KindOpening`, `KindBet`, `KindWin`, `KindLoss`, `KindRefund`, `KindRollback`), com `IsValid()`, `IsExternal()` e `IsReversal() bool`.
  - `Status` (`StatusPending`, `StatusPendingReference`, `StatusProcessed`, `StatusRejected`, `StatusFailed`), com `IsValid()`, `IsTerminal()` e `CanTransitionTo(next Status) bool`.
  - `Origin` (`OriginInternal`, `OriginExternal`).
  - Sentinelas `ErrInvalidTransaction` e `ErrInvalidTransition`.

- [ ] **Step 1: Escrever os testes que falham**

`internal/domain/wagering/failure_test.go`:

```go
//go:build !integration && !e2e

package wagering

import (
	"errors"
	"strings"
	"testing"
)

func TestFailureCodeCategories(t *testing.T) {
	correctable := []FailureCode{
		FailureMalformedPayload, FailureMissingField, FailureInvalidField, FailureInvalidID,
		FailureInvalidMoney, FailureUnsupportedCurrency, FailureUnknownKind, FailureKindNotAllowed,
		FailureInvalidAmountForKind, FailureMissingReference, FailureReferenceNotAllowed,
		FailureMissingIdempotencyKey, FailureInvalidIdempotencyKey,
	}
	business := []FailureCode{
		FailureInsufficientFunds, FailureInsufficientFundsForReversal, FailureWalletNotFound,
		FailureWalletPlayerMismatch, FailureCurrencyMismatch, FailureReferenceNotFound,
		FailureReferenceNotProcessed, FailureReferenceKindInvalid, FailureReferenceMismatch,
		FailureAmountMismatch, FailureReferenceAlreadyReversed,
	}
	other := []FailureCode{FailureInfrastructure, FailureProviderIdentityMismatch, FailureMessageIDReused}

	for _, c := range correctable {
		if c.Category() != CategoryCorrectable || c.IsBusinessRejection() {
			t.Errorf("%s: category %s business %v", c, c.Category(), c.IsBusinessRejection())
		}
	}
	for _, c := range business {
		if c.Category() != CategoryDefinitive || !c.IsBusinessRejection() {
			t.Errorf("%s: category %s business %v", c, c.Category(), c.IsBusinessRejection())
		}
	}
	for _, c := range other {
		if c.Category() != CategoryDefinitive || c.IsBusinessRejection() {
			t.Errorf("%s: category %s business %v", c, c.Category(), c.IsBusinessRejection())
		}
	}
	if FailureInsufficientFunds == FailureInsufficientFundsForReversal {
		t.Fatal("reversal without funds must have its own code")
	}
}

func TestInputError(t *testing.T) {
	var err error = &InputError{Violations: []Violation{
		{Code: FailureInvalidMoney, Field: "money.amount", Reason: "bad scale"},
		{Code: FailureMissingField, Field: "roundId", Reason: "is required"},
	}}
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatal("errors.As must find *InputError")
	}
	if ie.Code() != FailureInvalidMoney {
		t.Fatalf("Code() = %s, want first violation code", ie.Code())
	}
	if !strings.Contains(err.Error(), "money.amount") || !strings.Contains(err.Error(), "roundId") {
		t.Fatalf("Error() = %q", err.Error())
	}
}
```

`internal/domain/wagering/kind_test.go`:

```go
//go:build !integration && !e2e

package wagering

import "testing"

func TestKinds(t *testing.T) {
	for _, k := range []Kind{KindBet, KindWin, KindLoss, KindRefund, KindRollback} {
		if !k.IsValid() || !k.IsExternal() {
			t.Errorf("%s must be a valid external kind", k)
		}
	}
	if !KindOpening.IsValid() || KindOpening.IsExternal() {
		t.Error("OPENING is valid but internal only")
	}
	if Kind("JACKPOT").IsValid() {
		t.Error("unknown kind must be invalid")
	}
	if !KindRefund.IsReversal() || !KindRollback.IsReversal() || KindWin.IsReversal() {
		t.Error("only REFUND and ROLLBACK are reversals")
	}
}

func TestStatusTransitions(t *testing.T) {
	allowed := map[Status][]Status{
		StatusPending:          {StatusProcessed, StatusRejected, StatusPendingReference, StatusFailed},
		StatusPendingReference: {StatusProcessed, StatusRejected, StatusFailed},
	}
	all := []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed}
	for _, from := range all {
		for _, to := range all {
			want := false
			for _, a := range allowed[from] {
				if a == to {
					want = true
				}
			}
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("%s -> %s: got %v want %v", from, to, got, want)
			}
		}
	}
	for _, s := range []Status{StatusProcessed, StatusRejected, StatusFailed} {
		if !s.IsTerminal() {
			t.Errorf("%s must be terminal", s)
		}
	}
	for _, s := range []Status{StatusPending, StatusPendingReference} {
		if s.IsTerminal() {
			t.Errorf("%s must not be terminal", s)
		}
	}
	if Status("DONE").IsValid() {
		t.Error("unknown status must be invalid")
	}
}
```

- [ ] **Step 2: Rodar os testes e confirmar que falham**

Run: `go test -tags=unit ./internal/domain/wagering/...`
Expected: FAIL de compilação, `undefined: FailureMalformedPayload`.

- [ ] **Step 3: Implementar**

`internal/domain/wagering/errors.go`:

```go
package wagering

import "errors"

var (
	// ErrInvalidTransaction reports invalid construction or rehydration data.
	ErrInvalidTransaction = errors.New("wagering: invalid transaction")
	// ErrInvalidTransition reports a state change the state machine forbids.
	ErrInvalidTransition = errors.New("wagering: invalid state transition")
)
```

`internal/domain/wagering/failure.go`:

```go
// Package wagering models external wager operations (BET, WIN, LOSS, REFUND,
// ROLLBACK) and the internal OPENING, their state machine and business rules.
package wagering

import (
	"fmt"
	"strings"
)

// FailureCode is a stable, documented reason for a rejection or failure.
type FailureCode string

// Correctable input errors: HTTP 400, never persisted.
const (
	FailureMalformedPayload      FailureCode = "MALFORMED_PAYLOAD"
	FailureMissingField          FailureCode = "MISSING_FIELD"
	FailureInvalidField          FailureCode = "INVALID_FIELD"
	FailureInvalidID             FailureCode = "INVALID_ID"
	FailureInvalidMoney          FailureCode = "INVALID_MONEY"
	FailureUnsupportedCurrency   FailureCode = "UNSUPPORTED_CURRENCY"
	FailureUnknownKind           FailureCode = "UNKNOWN_KIND"
	FailureKindNotAllowed        FailureCode = "KIND_NOT_ALLOWED"
	FailureInvalidAmountForKind  FailureCode = "INVALID_AMOUNT_FOR_KIND"
	FailureMissingReference      FailureCode = "MISSING_REFERENCE"
	FailureReferenceNotAllowed   FailureCode = "REFERENCE_NOT_ALLOWED"
	FailureMissingIdempotencyKey FailureCode = "MISSING_IDEMPOTENCY_KEY"
	FailureInvalidIdempotencyKey FailureCode = "INVALID_IDEMPOTENCY_KEY"
)

// Definitive business rejections: persisted as REJECTED, HTTP 422.
const (
	FailureInsufficientFunds            FailureCode = "INSUFFICIENT_FUNDS"
	FailureInsufficientFundsForReversal FailureCode = "INSUFFICIENT_FUNDS_FOR_REVERSAL"
	FailureWalletNotFound               FailureCode = "WALLET_NOT_FOUND"
	FailureWalletPlayerMismatch         FailureCode = "WALLET_PLAYER_MISMATCH"
	FailureCurrencyMismatch             FailureCode = "CURRENCY_MISMATCH"
	FailureReferenceNotFound            FailureCode = "REFERENCE_NOT_FOUND"
	FailureReferenceNotProcessed        FailureCode = "REFERENCE_NOT_PROCESSED"
	FailureReferenceKindInvalid         FailureCode = "REFERENCE_KIND_INVALID"
	FailureReferenceMismatch            FailureCode = "REFERENCE_MISMATCH"
	FailureAmountMismatch               FailureCode = "AMOUNT_MISMATCH"
	FailureReferenceAlreadyReversed     FailureCode = "REFERENCE_ALREADY_REVERSED"
)

// Other definitive failures.
const (
	// FailureInfrastructure marks a permanent infrastructure failure (status FAILED).
	FailureInfrastructure FailureCode = "INFRASTRUCTURE_FAILURE"
	// FailureProviderIdentityMismatch: SQS sender is not the message providerId (DLQ).
	FailureProviderIdentityMismatch FailureCode = "PROVIDER_IDENTITY_MISMATCH"
	// FailureMessageIDReused: an SQS messageId was redelivered with another payload (DLQ).
	FailureMessageIDReused FailureCode = "MESSAGE_ID_REUSED"
)

// Category tells clients whether fixing the input can succeed.
type Category string

// Categories.
const (
	CategoryCorrectable Category = "CORRECTABLE"
	CategoryDefinitive  Category = "DEFINITIVE"
)

// Category returns CORRECTABLE for input errors and DEFINITIVE otherwise.
func (c FailureCode) Category() Category {
	switch c {
	case FailureMalformedPayload, FailureMissingField, FailureInvalidField, FailureInvalidID,
		FailureInvalidMoney, FailureUnsupportedCurrency, FailureUnknownKind, FailureKindNotAllowed,
		FailureInvalidAmountForKind, FailureMissingReference, FailureReferenceNotAllowed,
		FailureMissingIdempotencyKey, FailureInvalidIdempotencyKey:
		return CategoryCorrectable
	default:
		return CategoryDefinitive
	}
}

// IsBusinessRejection reports whether c is persisted as a REJECTED transaction.
func (c FailureCode) IsBusinessRejection() bool {
	switch c {
	case FailureInsufficientFunds, FailureInsufficientFundsForReversal, FailureWalletNotFound,
		FailureWalletPlayerMismatch, FailureCurrencyMismatch, FailureReferenceNotFound,
		FailureReferenceNotProcessed, FailureReferenceKindInvalid, FailureReferenceMismatch,
		FailureAmountMismatch, FailureReferenceAlreadyReversed:
		return true
	default:
		return false
	}
}

// Violation is one invalid input field.
type Violation struct {
	Code   FailureCode
	Field  string
	Reason string
}

// InputError reports correctable input errors. Nothing is persisted for it.
type InputError struct {
	Violations []Violation
}

// Error implements error.
func (e *InputError) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = fmt.Sprintf("%s: %s (%s)", v.Field, v.Reason, v.Code)
	}
	return "invalid input: " + strings.Join(parts, "; ")
}

// Code returns the failure code of the first violation.
func (e *InputError) Code() FailureCode {
	if len(e.Violations) == 0 {
		return FailureMalformedPayload
	}
	return e.Violations[0].Code
}
```

`internal/domain/wagering/kind.go`:

```go
package wagering

// Kind is the operation type.
type Kind string

// Kinds. OPENING is internal only.
const (
	KindOpening  Kind = "OPENING"
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
)

// IsValid reports whether k is a known kind.
func (k Kind) IsValid() bool { return k == KindOpening || k.IsExternal() }

// IsExternal reports whether k may arrive over HTTP or SQS.
func (k Kind) IsExternal() bool {
	switch k {
	case KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return true
	default:
		return false
	}
}

// IsReversal reports whether k reverses another transaction.
func (k Kind) IsReversal() bool { return k == KindRefund || k == KindRollback }

// Status is the processing state of a transaction.
type Status string

// Statuses. PROCESSED, REJECTED and FAILED are terminal.
const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

var transitions = map[Status][]Status{
	StatusPending:          {StatusProcessed, StatusRejected, StatusPendingReference, StatusFailed},
	StatusPendingReference: {StatusProcessed, StatusRejected, StatusFailed},
}

// IsValid reports whether s is a known status.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether s accepts no further transitions.
func (s Status) IsTerminal() bool {
	return s == StatusProcessed || s == StatusRejected || s == StatusFailed
}

// CanTransitionTo reports whether the state machine allows s → next.
func (s Status) CanTransitionTo(next Status) bool {
	for _, allowed := range transitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// Origin distinguishes the internal OPENING from external operations.
type Origin string

// Origins.
const (
	OriginInternal Origin = "INTERNAL"
	OriginExternal Origin = "EXTERNAL"
)
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -tags=unit ./internal/domain/wagering/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/wagering
git commit -m "feat(wagering): failure codes, kinds and state machine" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Command — validação de entrada e hash canônico

**Files:**

- Create: `internal/domain/wagering/command.go`
- Test: `internal/domain/wagering/command_test.go`

**Interfaces:**

- Consumes: `money.Parse`, `FailureCode`, `Violation`, `InputError` e `Kind`.
- Produces: `wagering.MaxFieldLength` (=255); `wagering.RawCommand` (todos os campos `string`: `ProviderID`, `ExternalTransactionID`, `IdempotencyKey`, `PlayerID`, `WalletID`, `RoundID`, `GameID`, `Kind`, `Amount`, `Currency`, `ReferenceExternalTransactionID`); `wagering.Command` (`ProviderID`, `ExternalTransactionID`, `IdempotencyKey`, `RoundID`, `GameID` e `ReferenceExternalTransactionID` como `string`; `PlayerID` e `WalletID` como `uuid.UUID`; `Kind Kind`; `Money money.Money`); `wagering.ParseCommand(raw RawCommand) (Command, error)` (o erro é `*InputError`); `(Command) PayloadHash() string`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/domain/wagering/command_test.go`:

```go
//go:build !integration && !e2e

package wagering

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func validRaw() RawCommand {
	return RawCommand{
		ProviderID:            "provider-a",
		ExternalTransactionID: "transaction-123",
		IdempotencyKey:        "provider-a:transaction-123",
		PlayerID:              "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		WalletID:              "0192f291-27dd-7d3f-8071-5f8685deef37",
		RoundID:               "round-987",
		GameID:                "fortune-chimp",
		Kind:                  "BET",
		Amount:                "25.00",
		Currency:              "BRL",
	}
}

func TestParseCommandValid(t *testing.T) {
	cmd, err := ParseCommand(validRaw())
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Kind != KindBet || cmd.Money.Amount() != "25.00" || cmd.PlayerID.String() != validRaw().PlayerID {
		t.Fatalf("cmd = %+v", cmd)
	}
}

func TestParseCommandViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *RawCommand)
		code   FailureCode
		field  string
	}{
		{"missing provider", func(r *RawCommand) { r.ProviderID = "" }, FailureMissingField, "providerId"},
		{"missing external id", func(r *RawCommand) { r.ExternalTransactionID = "" }, FailureMissingField, "externalTransactionId"},
		{"missing round", func(r *RawCommand) { r.RoundID = "" }, FailureMissingField, "roundId"},
		{"missing game", func(r *RawCommand) { r.GameID = "" }, FailureMissingField, "gameId"},
		{"too long game", func(r *RawCommand) { r.GameID = strings.Repeat("g", 256) }, FailureInvalidField, "gameId"},
		{"missing key", func(r *RawCommand) { r.IdempotencyKey = "" }, FailureMissingIdempotencyKey, "idempotencyKey"},
		{"too long key", func(r *RawCommand) { r.IdempotencyKey = strings.Repeat("k", 256) }, FailureInvalidIdempotencyKey, "idempotencyKey"},
		{"missing player", func(r *RawCommand) { r.PlayerID = "" }, FailureMissingField, "playerId"},
		{"uppercase uuid", func(r *RawCommand) { r.PlayerID = strings.ToUpper(r.PlayerID) }, FailureInvalidID, "playerId"},
		{"braced uuid", func(r *RawCommand) { r.WalletID = "{" + r.WalletID + "}" }, FailureInvalidID, "walletId"},
		{"nil uuid", func(r *RawCommand) { r.WalletID = "00000000-0000-0000-0000-000000000000" }, FailureInvalidID, "walletId"},
		{"garbage uuid", func(r *RawCommand) { r.WalletID = "wallet-1" }, FailureInvalidID, "walletId"},
		{"unknown kind", func(r *RawCommand) { r.Kind = "JACKPOT" }, FailureUnknownKind, "kind"},
		{"lowercase kind", func(r *RawCommand) { r.Kind = "bet" }, FailureUnknownKind, "kind"},
		{"opening", func(r *RawCommand) { r.Kind = "OPENING" }, FailureKindNotAllowed, "kind"},
		{"missing kind", func(r *RawCommand) { r.Kind = "" }, FailureMissingField, "kind"},
		{"missing amount", func(r *RawCommand) { r.Amount = "" }, FailureMissingField, "money.amount"},
		{"missing currency", func(r *RawCommand) { r.Currency = "" }, FailureMissingField, "money.currency"},
		{"bad amount", func(r *RawCommand) { r.Amount = "25.0" }, FailureInvalidMoney, "money.amount"},
		{"negative amount", func(r *RawCommand) { r.Amount = "-25.00" }, FailureInvalidMoney, "money.amount"},
		{"bad currency", func(r *RawCommand) { r.Currency = "JPY" }, FailureUnsupportedCurrency, "money.currency"},
		{"zero bet", func(r *RawCommand) { r.Amount = "0.00" }, FailureInvalidAmountForKind, "money.amount"},
		{"zero win", func(r *RawCommand) { r.Kind, r.Amount = "WIN", "0.00" }, FailureInvalidAmountForKind, "money.amount"},
		{"zero refund", func(r *RawCommand) {
			r.Kind, r.Amount, r.ReferenceExternalTransactionID = "REFUND", "0.00", "tx-0"
		}, FailureInvalidAmountForKind, "money.amount"},
		{"zero rollback", func(r *RawCommand) {
			r.Kind, r.Amount, r.ReferenceExternalTransactionID = "ROLLBACK", "0.00", "tx-0"
		}, FailureInvalidAmountForKind, "money.amount"},
		{"positive loss", func(r *RawCommand) { r.Kind, r.Amount = "LOSS", "1.00" }, FailureInvalidAmountForKind, "money.amount"},
		{"refund without ref", func(r *RawCommand) { r.Kind = "REFUND" }, FailureMissingReference, "referenceExternalTransactionId"},
		{"rollback without ref", func(r *RawCommand) { r.Kind = "ROLLBACK" }, FailureMissingReference, "referenceExternalTransactionId"},
		{"bet with ref", func(r *RawCommand) { r.ReferenceExternalTransactionID = "tx-0" }, FailureReferenceNotAllowed, "referenceExternalTransactionId"},
		{"loss with ref", func(r *RawCommand) {
			r.Kind, r.Amount, r.ReferenceExternalTransactionID = "LOSS", "0.00", "tx-0"
		}, FailureReferenceNotAllowed, "referenceExternalTransactionId"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validRaw()
			tt.mutate(&raw)
			_, err := ParseCommand(raw)
			var ie *InputError
			if !errors.As(err, &ie) {
				t.Fatalf("error = %v, want *InputError", err)
			}
			for _, v := range ie.Violations {
				if v.Code == tt.code && v.Field == tt.field {
					return
				}
			}
			t.Fatalf("violations %+v do not contain %s on %s", ie.Violations, tt.code, tt.field)
		})
	}
}

func TestParseCommandZeroPolicy(t *testing.T) {
	loss := validRaw()
	loss.Kind, loss.Amount = "LOSS", "0.00"
	if _, err := ParseCommand(loss); err != nil {
		t.Fatalf("LOSS 0.00 must be accepted: %v", err)
	}
	win := validRaw()
	win.Kind, win.ReferenceExternalTransactionID = "WIN", "transaction-100"
	if _, err := ParseCommand(win); err != nil {
		t.Fatalf("WIN with optional reference must be accepted: %v", err)
	}
}

func TestPayloadHashCanonicalJSON(t *testing.T) {
	cmd, err := ParseCommand(validRaw())
	if err != nil {
		t.Fatal(err)
	}
	canonical := `{"externalTransactionId":"transaction-123","gameId":"fortune-chimp","kind":"BET",` +
		`"money":{"amount":"25.00","currency":"BRL"},"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",` +
		`"providerId":"provider-a","roundId":"round-987","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37"}`
	sum := sha256.Sum256([]byte(canonical))
	if got, want := cmd.PayloadHash(), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("PayloadHash() = %s, want sha256(%s) = %s", got, canonical, want)
	}
}

func TestPayloadHashFields(t *testing.T) {
	base, _ := ParseCommand(validRaw())

	otherKey := validRaw()
	otherKey.IdempotencyKey = "something-else"
	k, _ := ParseCommand(otherKey)
	if k.PayloadHash() != base.PayloadHash() {
		t.Fatal("idempotency key must not affect the hash")
	}

	otherAmount := validRaw()
	otherAmount.Amount = "25.01"
	a, _ := ParseCommand(otherAmount)
	if a.PayloadHash() == base.PayloadHash() {
		t.Fatal("amount must affect the hash")
	}

	withRef := validRaw()
	withRef.Kind, withRef.ReferenceExternalTransactionID = "WIN", "transaction-100"
	withoutRef := validRaw()
	withoutRef.Kind = "WIN"
	r1, _ := ParseCommand(withRef)
	r2, _ := ParseCommand(withoutRef)
	if r1.PayloadHash() == r2.PayloadHash() {
		t.Fatal("reference must affect the hash when present")
	}

	html := validRaw()
	html.GameID = "a<b>&c"
	h, _ := ParseCommand(html)
	canonical := `{"externalTransactionId":"transaction-123","gameId":"a<b>&c","kind":"BET",` +
		`"money":{"amount":"25.00","currency":"BRL"},"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",` +
		`"providerId":"provider-a","roundId":"round-987","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37"}`
	sum := sha256.Sum256([]byte(canonical))
	if h.PayloadHash() != hex.EncodeToString(sum[:]) {
		t.Fatal("canonical JSON must not HTML-escape")
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/wagering/...`
Expected: FAIL de compilação, `undefined: RawCommand`.

- [ ] **Step 3: Implementar**

`internal/domain/wagering/command.go`:

```go
package wagering

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// MaxFieldLength bounds free-text identifiers and the idempotency key, in bytes.
const MaxFieldLength = 255

// RawCommand is an external operation exactly as received over HTTP or SQS.
// Absent optional fields are empty strings.
type RawCommand struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       string
	WalletID                       string
	RoundID                        string
	GameID                         string
	Kind                           string
	Amount                         string
	Currency                       string
	ReferenceExternalTransactionID string
}

// Command is a validated external operation. HTTP and SQS build the same
// Command, so idempotency and hashing are identical across channels.
type Command struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       uuid.UUID
	WalletID                       uuid.UUID
	RoundID                        string
	GameID                         string
	Kind                           Kind
	Money                          money.Money
	ReferenceExternalTransactionID string
}

// ParseCommand validates raw and returns a Command, or an *InputError listing
// every correctable violation. Values are never normalized: UUIDs must be
// lowercase canonical and money must be canonical, so the payload hash covers
// exactly what was received.
func ParseCommand(raw RawCommand) (Command, error) {
	var v violations
	v.requireText("providerId", raw.ProviderID)
	v.requireText("externalTransactionId", raw.ExternalTransactionID)
	v.requireText("roundId", raw.RoundID)
	v.requireText("gameId", raw.GameID)
	switch {
	case raw.IdempotencyKey == "":
		v.add(FailureMissingIdempotencyKey, "idempotencyKey", "is required")
	case len(raw.IdempotencyKey) > MaxFieldLength:
		v.add(FailureInvalidIdempotencyKey, "idempotencyKey", fmt.Sprintf("must be at most %d bytes", MaxFieldLength))
	}
	playerID := v.parseID("playerId", raw.PlayerID)
	walletID := v.parseID("walletId", raw.WalletID)
	kind, kindOK := v.parseKind(raw.Kind)
	amount, moneyOK := v.parseMoney(raw.Amount, raw.Currency)

	if kindOK && moneyOK {
		switch {
		case kind == KindLoss && !amount.IsZero():
			v.add(FailureInvalidAmountForKind, "money.amount", "LOSS requires 0.00")
		case kind != KindLoss && !amount.IsPositive():
			v.add(FailureInvalidAmountForKind, "money.amount", fmt.Sprintf("%s requires an amount greater than 0.00", kind))
		}
	}
	ref := raw.ReferenceExternalTransactionID
	if kindOK {
		switch {
		case kind.IsReversal() && ref == "":
			v.add(FailureMissingReference, "referenceExternalTransactionId", fmt.Sprintf("is required for %s", kind))
		case (kind == KindBet || kind == KindLoss) && ref != "":
			v.add(FailureReferenceNotAllowed, "referenceExternalTransactionId", fmt.Sprintf("is not allowed for %s", kind))
		}
	}
	if len(ref) > MaxFieldLength {
		v.add(FailureInvalidField, "referenceExternalTransactionId", fmt.Sprintf("must be at most %d bytes", MaxFieldLength))
	}
	if len(v) > 0 {
		return Command{}, &InputError{Violations: v}
	}
	return Command{
		ProviderID:                     raw.ProviderID,
		ExternalTransactionID:          raw.ExternalTransactionID,
		IdempotencyKey:                 raw.IdempotencyKey,
		PlayerID:                       playerID,
		WalletID:                       walletID,
		RoundID:                        raw.RoundID,
		GameID:                         raw.GameID,
		Kind:                           kind,
		Money:                          amount,
		ReferenceExternalTransactionID: ref,
	}, nil
}

// PayloadHash returns the hex SHA-256 of the canonical JSON of the business
// fields: keys sorted, no insignificant whitespace, no HTML escaping. The
// idempotency key and transport metadata are excluded, and
// referenceExternalTransactionId is omitted when absent.
func (c Command) PayloadHash() string {
	fields := map[string]any{
		"providerId":            c.ProviderID,
		"externalTransactionId": c.ExternalTransactionID,
		"playerId":              c.PlayerID.String(),
		"walletId":              c.WalletID.String(),
		"roundId":               c.RoundID,
		"gameId":                c.GameID,
		"kind":                  string(c.Kind),
		"money": map[string]any{
			"amount":   c.Money.Amount(),
			"currency": c.Money.Currency().Code(),
		},
	}
	if c.ReferenceExternalTransactionID != "" {
		fields["referenceExternalTransactionId"] = c.ReferenceExternalTransactionID
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encoding/json sorts map keys; strings and maps cannot fail to encode.
	_ = enc.Encode(fields)
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

type violations []Violation

func (v *violations) add(code FailureCode, field, reason string) {
	*v = append(*v, Violation{Code: code, Field: field, Reason: reason})
}

func (v *violations) requireText(field, value string) {
	switch {
	case value == "":
		v.add(FailureMissingField, field, "is required")
	case len(value) > MaxFieldLength:
		v.add(FailureInvalidField, field, fmt.Sprintf("must be at most %d bytes", MaxFieldLength))
	}
}

func (v *violations) parseID(field, value string) uuid.UUID {
	if value == "" {
		v.add(FailureMissingField, field, "is required")
		return uuid.Nil
	}
	id, err := uuid.Parse(value)
	if err != nil || id.String() != value || id == uuid.Nil {
		v.add(FailureInvalidID, field, "must be a non-nil lowercase canonical UUID")
		return uuid.Nil
	}
	return id
}

func (v *violations) parseKind(value string) (Kind, bool) {
	k := Kind(value)
	switch {
	case value == "":
		v.add(FailureMissingField, "kind", "is required")
	case k == KindOpening:
		v.add(FailureKindNotAllowed, "kind", "OPENING is reserved for internal wallet opening")
	case !k.IsExternal():
		v.add(FailureUnknownKind, "kind", "must be one of BET, WIN, LOSS, REFUND, ROLLBACK")
	default:
		return k, true
	}
	return "", false
}

func (v *violations) parseMoney(amount, currency string) (money.Money, bool) {
	missing := false
	if amount == "" {
		v.add(FailureMissingField, "money.amount", "is required")
		missing = true
	}
	if currency == "" {
		v.add(FailureMissingField, "money.currency", "is required")
		missing = true
	}
	if missing {
		return money.Money{}, false
	}
	m, err := money.Parse(amount, currency)
	switch {
	case err == nil:
		return m, true
	case errors.Is(err, money.ErrUnsupportedCurrency):
		v.add(FailureUnsupportedCurrency, "money.currency", "must be an ISO 4217 currency with 2 decimals")
	default:
		v.add(FailureInvalidMoney, "money.amount", err.Error())
	}
	return money.Money{}, false
}
```

- [ ] **Step 4: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/wagering/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/wagering
git commit -m "feat(wagering): command validation and canonical payload hash" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: WagerTransaction — criação, reidratação, OPENING e FAILED

**Files:**

- Create: `internal/domain/wagering/transaction.go`
- Test: `internal/domain/wagering/transaction_test.go`

**Interfaces:**

- Consumes: `Command`, `Kind`, `Status`, `Origin`, `FailureCode` e as sentinelas (Tasks 7–8); `wallet.Wallet`, `wallet.LedgerEntry` (Tasks 4–5); `events.*` (Task 6).
- Produces:
  - `wagering.WagerTransaction`.
  - Construtores: `NewExternal(id uuid.UUID, cmd Command, now time.Time) (*WagerTransaction, error)` e `NewOpening(id, walletID, playerID uuid.UUID, amount money.Money, now time.Time) (*WagerTransaction, error)`.
  - Transições: `(*WagerTransaction) CompleteOpening(w *wallet.Wallet, entry wallet.LedgerEntry, now time.Time) error` e `MarkFailed(now time.Time) error`.
  - Eventos: `PullEvents() []events.Data`.
  - Persistência: `State() State` e `Rehydrate(s State) (*WagerTransaction, error)`, onde `wagering.State` traz todos os campos persistidos (abaixo).
  - Getters: `ID()`, `WalletID()`, `ReferenceTransactionID() uuid.UUID`, `Kind() Kind`, `Status() Status`, `FailureCode() FailureCode`, `PayloadHash()`, `ProviderID()`, `ExternalTransactionID()`, `IdempotencyKey()`, `ReferenceExternalTransactionID() string`, `ResultBalance() money.Money`, `Attempts() int`, `NextAttemptAt() time.Time`.
  - Helpers internos usados na Task 10: `transition(next Status, now time.Time) error`, `reject(code FailureCode, now time.Time) error`, `complete(w *wallet.Wallet, entry *wallet.LedgerEntry, now time.Time) error`, `record(e events.Data)`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/domain/wagering/transaction_test.go`:

```go
//go:build !integration && !e2e

package wagering

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func brl(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewExternal(t *testing.T) {
	cmd, err := ParseCommand(validRaw())
	if err != nil {
		t.Fatal(err)
	}
	id := newID(t)
	tx, err := NewExternal(id, cmd, testNow)
	if err != nil {
		t.Fatal(err)
	}
	s := tx.State()
	if s.ID != id || s.Origin != OriginExternal || s.Status != StatusPending || s.Kind != KindBet ||
		s.PayloadHash != cmd.PayloadHash() || s.IdempotencyKey != cmd.IdempotencyKey ||
		s.ProviderID != "provider-a" || !s.CreatedAt.Equal(testNow) || s.Attempts != 0 {
		t.Fatalf("state = %+v", s)
	}
	if len(tx.PullEvents()) != 0 {
		t.Fatal("creation must not record events")
	}
	if _, err := NewExternal(uuid.Nil, cmd, testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("nil id error = %v", err)
	}
	if _, err := NewExternal(id, Command{}, testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("empty command error = %v", err)
	}
}

func TestOpeningLifecycle(t *testing.T) {
	walletID, playerID, txID, entryID := newID(t), newID(t), newID(t), newID(t)
	initial := brl(t, "1000.00")
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: walletID, PlayerID: playerID, InitialBalance: initial,
		OpeningTransactionID: txID, LedgerEntryID: entryID, Now: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewOpening(txID, walletID, playerID, initial, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CompleteOpening(w, *entry, testNow); err != nil {
		t.Fatal(err)
	}
	s := tx.State()
	if s.Status != StatusProcessed || s.Origin != OriginInternal || s.Kind != KindOpening ||
		!s.ResultBalance.Equal(initial) || s.ProviderID != "" || s.ExternalTransactionID != "" ||
		s.IdempotencyKey != "" || s.PayloadHash != "" || s.RoundID != "" || s.GameID != "" {
		t.Fatalf("opening state = %+v", s)
	}
	evs := tx.PullEvents()
	if len(evs) != 2 {
		t.Fatalf("events = %d, want Processed + BalanceChanged", len(evs))
	}
	processed, ok := evs[0].(events.WagerTransactionProcessed)
	if !ok || processed.Origin != "INTERNAL" || processed.Kind != "OPENING" || processed.ProviderID != "" ||
		!processed.BalanceAfter.Equal(initial) {
		t.Fatalf("processed event = %+v", evs[0])
	}
	changed, ok := evs[1].(events.WalletBalanceChanged)
	if !ok || changed.WalletVersion != 1 || changed.Direction != "CREDIT" || !changed.BalanceBefore.IsZero() ||
		!changed.BalanceAfter.Equal(initial) || changed.TransactionID != txID {
		t.Fatalf("balance changed event = %+v", evs[1])
	}
	if len(tx.PullEvents()) != 0 {
		t.Fatal("PullEvents must clear recorded events")
	}
}

func TestNewOpeningInvalid(t *testing.T) {
	if _, err := NewOpening(newID(t), newID(t), newID(t), brl(t, "0.00"), testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("zero opening error = %v, want ErrInvalidTransaction (zero balance creates no OPENING)", err)
	}
	if _, err := NewOpening(newID(t), uuid.Nil, newID(t), brl(t, "1.00"), testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("nil wallet error = %v", err)
	}
}

func TestMarkFailed(t *testing.T) {
	cmd, _ := ParseCommand(validRaw())
	tx, _ := NewExternal(newID(t), cmd, testNow)
	if err := tx.MarkFailed(testNow); err != nil {
		t.Fatal(err)
	}
	if tx.Status() != StatusFailed || tx.FailureCode() != FailureInfrastructure {
		t.Fatalf("status %s code %s", tx.Status(), tx.FailureCode())
	}
	if err := tx.MarkFailed(testNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second MarkFailed error = %v, want ErrInvalidTransition (terminal)", err)
	}
	if len(tx.PullEvents()) != 0 {
		t.Fatal("FAILED records no integration event")
	}
}

func TestRehydrateRoundTripAndNoEvents(t *testing.T) {
	cmd, _ := ParseCommand(validRaw())
	tx, _ := NewExternal(newID(t), cmd, testNow)
	if err := tx.MarkFailed(testNow); err != nil {
		t.Fatal(err)
	}
	back, err := Rehydrate(tx.State())
	if err != nil {
		t.Fatal(err)
	}
	if back.State() != tx.State() {
		t.Fatalf("round trip mismatch\n got %+v\nwant %+v", back.State(), tx.State())
	}
	if len(back.PullEvents()) != 0 {
		t.Fatal("rehydration must not record events")
	}
	if err := back.MarkFailed(testNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("rehydrated terminal transaction must reject transitions")
	}
}

func TestRehydrateInvalid(t *testing.T) {
	cmd, _ := ParseCommand(validRaw())
	tx, _ := NewExternal(newID(t), cmd, testNow)
	valid := tx.State()
	tests := []struct {
		name   string
		mutate func(s *State)
	}{
		{"nil id", func(s *State) { s.ID = uuid.Nil }},
		{"bad status", func(s *State) { s.Status = "DONE" }},
		{"bad kind", func(s *State) { s.Kind = "JACKPOT" }},
		{"external opening", func(s *State) { s.Kind = KindOpening }},
		{"internal bet", func(s *State) { s.Origin = OriginInternal }},
		{"missing provider", func(s *State) { s.ProviderID = "" }},
		{"missing hash", func(s *State) { s.PayloadHash = "" }},
		{"invalid money", func(s *State) { s.Money = money.Money{} }},
		{"rejected without code", func(s *State) { s.Status = StatusRejected }},
		{"processed without balance", func(s *State) { s.Status = StatusProcessed }},
		{"pending reference without next attempt", func(s *State) { s.Status = StatusPendingReference }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := valid
			tt.mutate(&s)
			if _, err := Rehydrate(s); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("error = %v, want ErrInvalidTransaction", err)
			}
		})
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha**

Run: `go test -tags=unit ./internal/domain/wagering/...`
Expected: FAIL de compilação, `undefined: NewExternal`.

- [ ] **Step 3: Implementar**

`internal/domain/wagering/transaction.go`:

```go
package wagering

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// WagerTransaction is one financial operation. External operations carry
// provider metadata and idempotency data; the internal OPENING does not.
// State changes only through validated transitions, which record the
// integration events to be written to the outbox.
type WagerTransaction struct {
	id                             uuid.UUID
	origin                         Origin
	kind                           Kind
	status                         Status
	walletID                       uuid.UUID
	playerID                       uuid.UUID
	money                          money.Money
	providerID                     string
	externalTransactionID          string
	idempotencyKey                 string
	payloadHash                    string
	roundID                        string
	gameID                         string
	referenceExternalTransactionID string
	referenceTransactionID         uuid.UUID
	referenceKind                  Kind
	failureCode                    FailureCode
	resultBalance                  money.Money
	attempts                       int
	nextAttemptAt                  time.Time
	createdAt                      time.Time
	updatedAt                      time.Time
	processedAt                    time.Time
	events                         []events.Data
}

// State is the full persisted form of a WagerTransaction. Optional values use
// zero values: uuid.Nil, "", the zero Money, the zero time.
type State struct {
	ID                             uuid.UUID
	Origin                         Origin
	Kind                           Kind
	Status                         Status
	WalletID                       uuid.UUID
	PlayerID                       uuid.UUID
	Money                          money.Money
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PayloadHash                    string
	RoundID                        string
	GameID                         string
	ReferenceExternalTransactionID string
	ReferenceTransactionID         uuid.UUID
	ReferenceKind                  Kind
	FailureCode                    FailureCode
	ResultBalance                  money.Money
	Attempts                       int
	NextAttemptAt                  time.Time
	CreatedAt                      time.Time
	UpdatedAt                      time.Time
	ProcessedAt                    time.Time
}

// NewExternal creates a PENDING external operation from a validated command.
func NewExternal(id uuid.UUID, cmd Command, now time.Time) (*WagerTransaction, error) {
	switch {
	case id == uuid.Nil || now.IsZero():
		return nil, fmt.Errorf("%w: id and now are required", ErrInvalidTransaction)
	case !cmd.Kind.IsExternal():
		return nil, fmt.Errorf("%w: kind %q is not external", ErrInvalidTransaction, cmd.Kind)
	case cmd.WalletID == uuid.Nil || cmd.PlayerID == uuid.Nil || !cmd.Money.IsValid():
		return nil, fmt.Errorf("%w: wallet, player and money are required", ErrInvalidTransaction)
	case cmd.ProviderID == "" || cmd.ExternalTransactionID == "" || cmd.IdempotencyKey == "" ||
		cmd.RoundID == "" || cmd.GameID == "":
		return nil, fmt.Errorf("%w: external metadata is required", ErrInvalidTransaction)
	}
	now = now.UTC()
	return &WagerTransaction{
		id:                             id,
		origin:                         OriginExternal,
		kind:                           cmd.Kind,
		status:                         StatusPending,
		walletID:                       cmd.WalletID,
		playerID:                       cmd.PlayerID,
		money:                          cmd.Money,
		providerID:                     cmd.ProviderID,
		externalTransactionID:          cmd.ExternalTransactionID,
		idempotencyKey:                 cmd.IdempotencyKey,
		payloadHash:                    cmd.PayloadHash(),
		roundID:                        cmd.RoundID,
		gameID:                         cmd.GameID,
		referenceExternalTransactionID: cmd.ReferenceExternalTransactionID,
		createdAt:                      now,
		updatedAt:                      now,
	}, nil
}

// NewOpening creates the PENDING internal OPENING credit of a wallet opened
// with a positive balance. A zero initial balance creates no OPENING.
func NewOpening(id, walletID, playerID uuid.UUID, amount money.Money, now time.Time) (*WagerTransaction, error) {
	if id == uuid.Nil || walletID == uuid.Nil || playerID == uuid.Nil || now.IsZero() {
		return nil, fmt.Errorf("%w: id, walletId, playerId and now are required", ErrInvalidTransaction)
	}
	if !amount.IsPositive() {
		return nil, fmt.Errorf("%w: OPENING requires a positive amount", ErrInvalidTransaction)
	}
	now = now.UTC()
	return &WagerTransaction{
		id:        id,
		origin:    OriginInternal,
		kind:      KindOpening,
		status:    StatusPending,
		walletID:  walletID,
		playerID:  playerID,
		money:     amount,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// CompleteOpening marks the OPENING as PROCESSED once the wallet was opened
// with entry, recording WagerTransactionProcessed and WalletBalanceChanged.
func (t *WagerTransaction) CompleteOpening(w *wallet.Wallet, entry wallet.LedgerEntry, now time.Time) error {
	if t.kind != KindOpening {
		return fmt.Errorf("%w: CompleteOpening on %s", ErrInvalidTransition, t.kind)
	}
	if w.ID() != t.walletID || entry.TransactionID() != t.id || entry.WalletID() != t.walletID {
		return fmt.Errorf("%w: wallet or entry does not belong to this OPENING", ErrInvalidTransaction)
	}
	return t.complete(w, &entry, now)
}

// MarkFailed records a permanent infrastructure failure for audit.
func (t *WagerTransaction) MarkFailed(now time.Time) error {
	if err := t.transition(StatusFailed, now); err != nil {
		return err
	}
	t.failureCode = FailureInfrastructure
	t.processedAt = t.updatedAt
	t.nextAttemptAt = time.Time{}
	return nil
}

// PullEvents returns the events recorded since the last call and clears them.
func (t *WagerTransaction) PullEvents() []events.Data {
	evs := t.events
	t.events = nil
	return evs
}

// Rehydrate rebuilds a stored transaction without transitions or events.
func Rehydrate(s State) (*WagerTransaction, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &WagerTransaction{
		id:                             s.ID,
		origin:                         s.Origin,
		kind:                           s.Kind,
		status:                         s.Status,
		walletID:                       s.WalletID,
		playerID:                       s.PlayerID,
		money:                          s.Money,
		providerID:                     s.ProviderID,
		externalTransactionID:          s.ExternalTransactionID,
		idempotencyKey:                 s.IdempotencyKey,
		payloadHash:                    s.PayloadHash,
		roundID:                        s.RoundID,
		gameID:                         s.GameID,
		referenceExternalTransactionID: s.ReferenceExternalTransactionID,
		referenceTransactionID:         s.ReferenceTransactionID,
		referenceKind:                  s.ReferenceKind,
		failureCode:                    s.FailureCode,
		resultBalance:                  s.ResultBalance,
		attempts:                       s.Attempts,
		nextAttemptAt:                  s.NextAttemptAt,
		createdAt:                      s.CreatedAt,
		updatedAt:                      s.UpdatedAt,
		processedAt:                    s.ProcessedAt,
	}, nil
}

func (s State) validate() error {
	fail := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidTransaction, reason) }
	switch {
	case s.ID == uuid.Nil || s.WalletID == uuid.Nil || s.PlayerID == uuid.Nil:
		return fail("id, walletId and playerId are required")
	case !s.Kind.IsValid() || !s.Status.IsValid():
		return fail("unknown kind or status")
	case !s.Money.IsValid() || s.Money.IsNegative():
		return fail("money must be valid and non-negative")
	case s.CreatedAt.IsZero() || s.UpdatedAt.IsZero():
		return fail("timestamps are required")
	case s.Attempts < 0:
		return fail("attempts must be non-negative")
	}
	switch s.Origin {
	case OriginInternal:
		if s.Kind != KindOpening || s.ProviderID != "" || s.ExternalTransactionID != "" || s.IdempotencyKey != "" ||
			s.PayloadHash != "" || s.RoundID != "" || s.GameID != "" || s.ReferenceExternalTransactionID != "" {
			return fail("internal origin must be an OPENING without external metadata")
		}
	case OriginExternal:
		if !s.Kind.IsExternal() || s.ProviderID == "" || s.ExternalTransactionID == "" || s.IdempotencyKey == "" ||
			s.PayloadHash == "" || s.RoundID == "" || s.GameID == "" {
			return fail("external origin requires an external kind and metadata")
		}
	default:
		return fail("unknown origin")
	}
	switch s.Status {
	case StatusProcessed:
		if !s.ResultBalance.IsValid() || s.FailureCode != "" {
			return fail("PROCESSED requires a result balance and no failure code")
		}
	case StatusRejected, StatusFailed:
		if s.FailureCode == "" {
			return fail("REJECTED/FAILED requires a failure code")
		}
	case StatusPendingReference:
		if s.NextAttemptAt.IsZero() {
			return fail("PENDING_REFERENCE requires nextAttemptAt")
		}
	}
	return nil
}

// State returns the persisted form of t.
func (t *WagerTransaction) State() State {
	return State{
		ID:                             t.id,
		Origin:                         t.origin,
		Kind:                           t.kind,
		Status:                         t.status,
		WalletID:                       t.walletID,
		PlayerID:                       t.playerID,
		Money:                          t.money,
		ProviderID:                     t.providerID,
		ExternalTransactionID:          t.externalTransactionID,
		IdempotencyKey:                 t.idempotencyKey,
		PayloadHash:                    t.payloadHash,
		RoundID:                        t.roundID,
		GameID:                         t.gameID,
		ReferenceExternalTransactionID: t.referenceExternalTransactionID,
		ReferenceTransactionID:         t.referenceTransactionID,
		ReferenceKind:                  t.referenceKind,
		FailureCode:                    t.failureCode,
		ResultBalance:                  t.resultBalance,
		Attempts:                       t.attempts,
		NextAttemptAt:                  t.nextAttemptAt,
		CreatedAt:                      t.createdAt,
		UpdatedAt:                      t.updatedAt,
		ProcessedAt:                    t.processedAt,
	}
}

// ID returns the internal transaction id.
func (t *WagerTransaction) ID() uuid.UUID { return t.id }

// WalletID returns the wallet the operation targets.
func (t *WagerTransaction) WalletID() uuid.UUID { return t.walletID }

// Kind returns the operation kind.
func (t *WagerTransaction) Kind() Kind { return t.kind }

// Status returns the current status.
func (t *WagerTransaction) Status() Status { return t.status }

// FailureCode returns the rejection or failure code, if any.
func (t *WagerTransaction) FailureCode() FailureCode { return t.failureCode }

// PayloadHash returns the canonical business payload hash (external only).
func (t *WagerTransaction) PayloadHash() string { return t.payloadHash }

// ProviderID returns the provider (external only).
func (t *WagerTransaction) ProviderID() string { return t.providerID }

// ExternalTransactionID returns the provider's transaction id (external only).
func (t *WagerTransaction) ExternalTransactionID() string { return t.externalTransactionID }

// IdempotencyKey returns the key the operation was received with (external only).
func (t *WagerTransaction) IdempotencyKey() string { return t.idempotencyKey }

// ReferenceExternalTransactionID returns the requested reference, if any.
func (t *WagerTransaction) ReferenceExternalTransactionID() string {
	return t.referenceExternalTransactionID
}

// ReferenceTransactionID returns the resolved internal reference, or uuid.Nil.
func (t *WagerTransaction) ReferenceTransactionID() uuid.UUID { return t.referenceTransactionID }

// ResultBalance returns the balance observed when the operation was
// PROCESSED; it is the zero Money otherwise.
func (t *WagerTransaction) ResultBalance() money.Money { return t.resultBalance }

// Attempts returns how many reference resolutions failed so far.
func (t *WagerTransaction) Attempts() int { return t.attempts }

// NextAttemptAt returns when the reference worker retries; zero if none.
func (t *WagerTransaction) NextAttemptAt() time.Time { return t.nextAttemptAt }

func (t *WagerTransaction) transition(next Status, now time.Time) error {
	if !t.status.CanTransitionTo(next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.status, next)
	}
	t.status = next
	t.updatedAt = now.UTC()
	return nil
}

func (t *WagerTransaction) record(e events.Data) { t.events = append(t.events, e) }

// complete marks t PROCESSED, snapshots the wallet balance and records the
// events. entry is nil when the balance did not move (LOSS).
func (t *WagerTransaction) complete(w *wallet.Wallet, entry *wallet.LedgerEntry, now time.Time) error {
	if err := t.transition(StatusProcessed, now); err != nil {
		return err
	}
	t.resultBalance = w.Balance()
	t.processedAt = t.updatedAt
	t.nextAttemptAt = time.Time{}
	processed := events.WagerTransactionProcessed{
		TransactionID:         t.id,
		WalletID:              t.walletID,
		PlayerID:              t.playerID,
		Origin:                string(t.origin),
		Kind:                  string(t.kind),
		Money:                 t.money,
		ProviderID:            t.providerID,
		ExternalTransactionID: t.externalTransactionID,
		RoundID:               t.roundID,
		GameID:                t.gameID,
		BalanceAfter:          t.resultBalance,
	}
	if t.referenceTransactionID != uuid.Nil {
		ref := t.referenceTransactionID
		processed.ReferenceTransactionID = &ref
	}
	t.record(processed)
	if entry != nil {
		t.record(events.WalletBalanceChanged{
			WalletID:      entry.WalletID(),
			TransactionID: entry.TransactionID(),
			Direction:     string(entry.Direction()),
			Money:         entry.Amount(),
			BalanceBefore: entry.BalanceBefore(),
			BalanceAfter:  entry.BalanceAfter(),
			WalletVersion: w.Version(),
		})
	}
	return nil
}

// reject marks t REJECTED with code and records WagerTransactionRejected.
func (t *WagerTransaction) reject(code FailureCode, now time.Time) error {
	if err := t.transition(StatusRejected, now); err != nil {
		return err
	}
	t.failureCode = code
	t.processedAt = t.updatedAt
	t.nextAttemptAt = time.Time{}
	t.record(events.WagerTransactionRejected{
		TransactionID:         t.id,
		WalletID:              t.walletID,
		ProviderID:            t.providerID,
		ExternalTransactionID: t.externalTransactionID,
		Kind:                  string(t.kind),
		Money:                 t.money,
		FailureCode:           string(code),
	})
	return nil
}
```

- [ ] **Step 4: Rodar o teste e confirmar que passa**

Run: `go test -race -tags=unit ./internal/domain/wagering/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/wagering
git commit -m "feat(wagering): transaction entity with opening, failure and rehydration" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Regras dos 5 tipos — Process, referências e RetryPolicy

**Files:**

- Create: `internal/domain/wagering/retry.go`, `internal/domain/wagering/process.go`
- Test: `internal/domain/wagering/retry_test.go`, `internal/domain/wagering/process_test.go`

**Interfaces:**

- Consumes: os helpers internos `transition`, `reject`, `complete` e `record` (Task 9); `wallet.Wallet.Debit` e `Credit` (Task 5).
- Produces:
  - Política de retentativa: `wagering.RetryPolicy{BaseDelay, MaxDelay time.Duration; MaxAttempts int}`, `DefaultRetryPolicy() RetryPolicy` (2s, 5min, 10), `(RetryPolicy) Validate() error` e `(RetryPolicy) Delay(attempts int) time.Duration`.
  - Referência: `wagering.Reference{ID uuid.UUID; Kind Kind; Status Status; WalletID, PlayerID uuid.UUID; RoundID string; Money money.Money}` e `(*WagerTransaction) AsReference() Reference`.
  - Processamento: `wagering.ProcessInput{Wallet *wallet.Wallet; Reference *Reference; ReferenceAlreadyReversed bool; LedgerEntryID uuid.UUID; RetryPolicy RetryPolicy; Now time.Time}` e `(*WagerTransaction) Process(in ProcessInput) (*wallet.LedgerEntry, error)`.
  - Contrato do chamador (Plano 3):
    - Travar a carteira antes. Passar `Wallet=nil` quando ela não existe.
    - Passar `Reference=nil` quando a referência não existe ou não foi informada.
    - Passar `ReferenceAlreadyReversed=true` quando já existe uma reversão `PROCESSED` do mesmo kind sobre a referência, ou, se a referência é uma BET, qualquer REFUND ou ROLLBACK `PROCESSED` sobre ela.
    - Um retorno de erro significa erro de programação ou infraestrutura (ex.: `money.ErrOverflow`), não rejeição. Rejeições ficam no status.

- [ ] **Step 1: Escrever os testes que falham**

`internal/domain/wagering/retry_test.go`:

```go
//go:build !integration && !e2e

package wagering

import (
	"testing"
	"time"
)

func TestRetryPolicyDelay(t *testing.T) {
	p := DefaultRetryPolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, 64 * time.Second, 128 * time.Second, 256 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for i, w := range want {
		if got := p.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %s, want %s", i+1, got, w)
		}
	}
	if got := p.Delay(1000); got != 5*time.Minute {
		t.Errorf("Delay(1000) = %s, want cap", got)
	}
}

func TestRetryPolicyValidate(t *testing.T) {
	for _, p := range []RetryPolicy{
		{BaseDelay: 0, MaxDelay: time.Second, MaxAttempts: 1},
		{BaseDelay: time.Second, MaxDelay: time.Millisecond, MaxAttempts: 1},
		{BaseDelay: time.Second, MaxDelay: time.Second, MaxAttempts: 0},
	} {
		if p.Validate() == nil {
			t.Errorf("Validate(%+v) = nil, want error", p)
		}
	}
}
```

`internal/domain/wagering/process_test.go`:

```go
//go:build !integration && !e2e

package wagering

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

type fixture struct {
	t      *testing.T
	wallet *wallet.Wallet
}

func newFixture(t *testing.T, balance string) *fixture {
	t.Helper()
	w, _, err := wallet.Open(wallet.OpenParams{
		ID: newID(t), PlayerID: newID(t), InitialBalance: brl(t, balance),
		OpeningTransactionID: newID(t), LedgerEntryID: newID(t), Now: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, wallet: w}
}

// op builds a PENDING external transaction against the fixture wallet.
func (f *fixture) op(kind Kind, amount, ref string) *WagerTransaction {
	f.t.Helper()
	ext := newID(f.t).String()
	raw := RawCommand{
		ProviderID: "provider-a", ExternalTransactionID: ext, IdempotencyKey: "provider-a:" + ext,
		PlayerID: f.wallet.PlayerID().String(), WalletID: f.wallet.ID().String(),
		RoundID: "round-1", GameID: "game-1", Kind: string(kind), Amount: amount, Currency: "BRL",
		ReferenceExternalTransactionID: ref,
	}
	cmd, err := ParseCommand(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	tx, err := NewExternal(newID(f.t), cmd, testNow)
	if err != nil {
		f.t.Fatal(err)
	}
	return tx
}

func (f *fixture) input(ref *Reference, reversed bool) ProcessInput {
	return ProcessInput{
		Wallet: f.wallet, Reference: ref, ReferenceAlreadyReversed: reversed,
		LedgerEntryID: newID(f.t), RetryPolicy: DefaultRetryPolicy(), Now: testNow.Add(time.Minute),
	}
}

// processed runs tx to PROCESSED and returns it as a reference.
func (f *fixture) processed(kind Kind, amount string, ref *Reference) Reference {
	f.t.Helper()
	refExt := ""
	if ref != nil {
		refExt = "ref"
	}
	tx := f.op(kind, amount, refExt)
	if _, err := tx.Process(f.input(ref, false)); err != nil {
		f.t.Fatal(err)
	}
	if tx.Status() != StatusProcessed {
		f.t.Fatalf("setup %s: status %s code %s", kind, tx.Status(), tx.FailureCode())
	}
	tx.PullEvents()
	return tx.AsReference()
}

func eventTypes(evs []events.Data) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventType()
	}
	return out
}

func assertOutcome(t *testing.T, tx *WagerTransaction, status Status, code FailureCode) {
	t.Helper()
	if tx.Status() != status || tx.FailureCode() != code {
		t.Fatalf("status %s code %q, want %s %q", tx.Status(), tx.FailureCode(), status, code)
	}
}

func TestBet(t *testing.T) {
	f := newFixture(t, "100.00")
	tx := f.op(KindBet, "25.00", "")
	entry, err := tx.Process(f.input(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, tx, StatusProcessed, "")
	if entry == nil || entry.Direction() != wallet.DirectionDebit || f.wallet.Balance().Amount() != "75.00" ||
		f.wallet.Version() != 2 || tx.ResultBalance().Amount() != "75.00" {
		t.Fatalf("entry %+v balance %s version %d", entry, f.wallet.Balance(), f.wallet.Version())
	}
	got := eventTypes(tx.PullEvents())
	if len(got) != 2 || got[0] != events.TypeWagerTransactionProcessed || got[1] != events.TypeWalletBalanceChanged {
		t.Fatalf("events = %v", got)
	}
}

func TestBetInsufficientFunds(t *testing.T) {
	f := newFixture(t, "100.00")
	first, second := f.op(KindBet, "80.00", ""), f.op(KindBet, "80.00", "")
	if _, err := first.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	entry, err := second.Process(f.input(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, first, StatusProcessed, "")
	assertOutcome(t, second, StatusRejected, FailureInsufficientFunds)
	if entry != nil || f.wallet.Balance().Amount() != "20.00" || f.wallet.Version() != 2 {
		t.Fatalf("balance %s version %d", f.wallet.Balance(), f.wallet.Version())
	}
	if got := eventTypes(second.PullEvents()); len(got) != 1 || got[0] != events.TypeWagerTransactionRejected {
		t.Fatalf("events = %v", got)
	}
}

func TestLossDoesNotMoveBalance(t *testing.T) {
	f := newFixture(t, "100.00")
	tx := f.op(KindLoss, "0.00", "")
	entry, err := tx.Process(f.input(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, tx, StatusProcessed, "")
	if entry != nil || f.wallet.Version() != 1 || tx.ResultBalance().Amount() != "100.00" {
		t.Fatalf("LOSS moved the wallet: entry %+v version %d", entry, f.wallet.Version())
	}
	if got := eventTypes(tx.PullEvents()); len(got) != 1 || got[0] != events.TypeWagerTransactionProcessed {
		t.Fatalf("events = %v, want only Processed", got)
	}
}

func TestWin(t *testing.T) {
	f := newFixture(t, "100.00")
	plain := f.op(KindWin, "50.00", "")
	if _, err := plain.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, plain, StatusProcessed, "")
	if f.wallet.Balance().Amount() != "150.00" {
		t.Fatalf("balance %s", f.wallet.Balance())
	}

	bet := f.processed(KindBet, "10.00", nil)
	withRef := f.op(KindWin, "35.00", "bet")
	if _, err := withRef.Process(f.input(&bet, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, withRef, StatusProcessed, "")
	if withRef.ReferenceTransactionID() != bet.ID {
		t.Fatal("WIN must persist the resolved reference")
	}

	win := f.processed(KindWin, "5.00", nil)
	badKind := f.op(KindWin, "5.00", "win")
	if _, err := badKind.Process(f.input(&win, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, badKind, StatusRejected, FailureReferenceKindInvalid)
}

func TestRefund(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		mutate   func(r *Reference)
		reversed bool
		status   Status
		code     FailureCode
	}{
		{"processed bet", "30.00", nil, false, StatusProcessed, ""},
		{"already reversed", "30.00", nil, true, StatusRejected, FailureReferenceAlreadyReversed},
		{"amount mismatch", "29.99", nil, false, StatusRejected, FailureAmountMismatch},
		{"round mismatch", "30.00", func(r *Reference) { r.RoundID = "other" }, false, StatusRejected, FailureReferenceMismatch},
		{"player mismatch", "30.00", func(r *Reference) { r.PlayerID = uuid.New() }, false, StatusRejected, FailureReferenceMismatch},
		{"wallet mismatch", "30.00", func(r *Reference) { r.WalletID = uuid.New() }, false, StatusRejected, FailureReferenceMismatch},
		{"reference rejected", "30.00", func(r *Reference) { r.Status = StatusRejected }, false, StatusRejected, FailureReferenceNotProcessed},
		{"reference failed", "30.00", func(r *Reference) { r.Status = StatusFailed }, false, StatusRejected, FailureReferenceNotProcessed},
		{"reference is a win", "30.00", func(r *Reference) { r.Kind = KindWin }, false, StatusRejected, FailureReferenceKindInvalid},
		{"reference still pending", "30.00", func(r *Reference) { r.Status = StatusPendingReference }, false, StatusPendingReference, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "100.00")
			bet := f.processed(KindBet, "30.00", nil)
			if tt.mutate != nil {
				tt.mutate(&bet)
			}
			before := f.wallet.Balance()
			tx := f.op(KindRefund, tt.amount, "bet")
			if _, err := tx.Process(f.input(&bet, tt.reversed)); err != nil {
				t.Fatal(err)
			}
			assertOutcome(t, tx, tt.status, tt.code)
			if tt.status == StatusProcessed {
				if f.wallet.Balance().Amount() != "100.00" {
					t.Fatalf("refund balance %s, want 100.00", f.wallet.Balance())
				}
			} else if !f.wallet.Balance().Equal(before) {
				t.Fatal("non-processed refund moved the balance")
			}
		})
	}
}

func TestRollback(t *testing.T) {
	t.Run("of bet credits", func(t *testing.T) {
		f := newFixture(t, "100.00")
		bet := f.processed(KindBet, "40.00", nil)
		tx := f.op(KindRollback, "40.00", "bet")
		entry, err := tx.Process(f.input(&bet, false))
		if err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusProcessed, "")
		if entry.Direction() != wallet.DirectionCredit || f.wallet.Balance().Amount() != "100.00" {
			t.Fatalf("direction %s balance %s", entry.Direction(), f.wallet.Balance())
		}
	})
	t.Run("of win debits", func(t *testing.T) {
		f := newFixture(t, "100.00")
		win := f.processed(KindWin, "50.00", nil)
		tx := f.op(KindRollback, "50.00", "win")
		entry, err := tx.Process(f.input(&win, false))
		if err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusProcessed, "")
		if entry.Direction() != wallet.DirectionDebit || f.wallet.Balance().Amount() != "100.00" {
			t.Fatalf("direction %s balance %s", entry.Direction(), f.wallet.Balance())
		}
	})
	t.Run("of refund debits", func(t *testing.T) {
		f := newFixture(t, "100.00")
		bet := f.processed(KindBet, "30.00", nil)
		refund := f.processed(KindRefund, "30.00", &bet)
		tx := f.op(KindRollback, "30.00", "refund")
		if _, err := tx.Process(f.input(&refund, false)); err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusProcessed, "")
		if f.wallet.Balance().Amount() != "70.00" {
			t.Fatalf("balance %s, want 70.00 (bet stands again)", f.wallet.Balance())
		}
	})
	t.Run("of win without funds", func(t *testing.T) {
		f := newFixture(t, "0.00")
		win := f.processed(KindWin, "50.00", nil)
		f.processed(KindBet, "45.00", nil)
		tx := f.op(KindRollback, "50.00", "win")
		if _, err := tx.Process(f.input(&win, false)); err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusRejected, FailureInsufficientFundsForReversal)
		if f.wallet.Balance().Amount() != "5.00" {
			t.Fatalf("balance %s", f.wallet.Balance())
		}
	})
	t.Run("of loss is invalid", func(t *testing.T) {
		f := newFixture(t, "100.00")
		loss := f.processed(KindLoss, "0.00", nil)
		loss.Money = brl(t, "10.00")
		tx := f.op(KindRollback, "10.00", "loss")
		if _, err := tx.Process(f.input(&loss, false)); err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusRejected, FailureReferenceKindInvalid)
	})
}

func TestWalletChecks(t *testing.T) {
	f := newFixture(t, "100.00")
	missing := f.op(KindBet, "10.00", "")
	in := f.input(nil, false)
	in.Wallet = nil
	if _, err := missing.Process(in); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, missing, StatusRejected, FailureWalletNotFound)

	other, _, _ := wallet.Open(wallet.OpenParams{
		ID: f.wallet.ID(), PlayerID: newID(t), InitialBalance: brl(t, "0.00"), Now: testNow,
	})
	wrongPlayer := f.op(KindBet, "10.00", "")
	in = f.input(nil, false)
	in.Wallet = other
	if _, err := wrongPlayer.Process(in); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, wrongPlayer, StatusRejected, FailureWalletPlayerMismatch)

	usd, _ := money.Parse("100.00", "USD")
	usdWallet, _, _ := wallet.Open(wallet.OpenParams{
		ID: f.wallet.ID(), PlayerID: f.wallet.PlayerID(), InitialBalance: usd,
		OpeningTransactionID: newID(t), LedgerEntryID: newID(t), Now: testNow,
	})
	wrongCurrency := f.op(KindBet, "10.00", "")
	in = f.input(nil, false)
	in.Wallet = usdWallet
	if _, err := wrongCurrency.Process(in); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, wrongCurrency, StatusRejected, FailureCurrencyMismatch)
	if usdWallet.Version() != 1 {
		t.Fatal("currency mismatch must not move the wallet")
	}
}

func TestPendingReferenceLifecycle(t *testing.T) {
	f := newFixture(t, "100.00")
	refund := f.op(KindRefund, "30.00", "bet-not-yet")
	first := f.input(nil, false)
	if _, err := refund.Process(first); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, refund, StatusPendingReference, "")
	if refund.Attempts() != 1 || !refund.NextAttemptAt().Equal(first.Now.Add(2*time.Second)) {
		t.Fatalf("attempts %d next %s", refund.Attempts(), refund.NextAttemptAt())
	}
	evs := refund.PullEvents()
	if got := eventTypes(evs); len(got) != 1 || got[0] != events.TypeWagerTransactionPendingReference {
		t.Fatalf("events = %v", got)
	}
	if p := evs[0].(events.WagerTransactionPendingReference); p.ReferenceExternalTransactionID != "bet-not-yet" || p.Attempts != 1 {
		t.Fatalf("pending event = %+v", p)
	}

	second := f.input(nil, false)
	if _, err := refund.Process(second); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, refund, StatusPendingReference, "")
	if refund.Attempts() != 2 || !refund.NextAttemptAt().Equal(second.Now.Add(4*time.Second)) {
		t.Fatalf("attempts %d next %s", refund.Attempts(), refund.NextAttemptAt())
	}
	if len(refund.PullEvents()) != 0 {
		t.Fatal("a retry must not emit a new pending event")
	}

	bet := f.processed(KindBet, "30.00", nil)
	if _, err := refund.Process(f.input(&bet, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, refund, StatusProcessed, "")
	if f.wallet.Balance().Amount() != "100.00" || !refund.NextAttemptAt().IsZero() {
		t.Fatalf("balance %s next %s", f.wallet.Balance(), refund.NextAttemptAt())
	}
}

func TestPendingReferenceExhaustion(t *testing.T) {
	policy := RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute, MaxAttempts: 3}

	f := newFixture(t, "100.00")
	missing := f.op(KindRollback, "30.00", "never")
	for i := 0; i < 3; i++ {
		in := f.input(nil, false)
		in.RetryPolicy = policy
		if _, err := missing.Process(in); err != nil {
			t.Fatal(err)
		}
	}
	assertOutcome(t, missing, StatusRejected, FailureReferenceNotFound)
	if got := eventTypes(missing.PullEvents()); len(got) != 2 || got[1] != events.TypeWagerTransactionRejected {
		t.Fatalf("events = %v, want PendingReference then Rejected", got)
	}

	stuck := f.op(KindRefund, "30.00", "stuck")
	pendingRef := Reference{ID: newID(t), Kind: KindBet, Status: StatusPendingReference,
		WalletID: f.wallet.ID(), PlayerID: f.wallet.PlayerID(), RoundID: "round-1", Money: brl(t, "30.00")}
	for i := 0; i < 3; i++ {
		in := f.input(&pendingRef, false)
		in.RetryPolicy = policy
		if _, err := stuck.Process(in); err != nil {
			t.Fatal(err)
		}
	}
	assertOutcome(t, stuck, StatusRejected, FailureReferenceNotProcessed)
}

func TestProcessTerminalIsRejected(t *testing.T) {
	f := newFixture(t, "100.00")
	tx := f.op(KindBet, "10.00", "")
	if _, err := tx.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	balance, version := f.wallet.Balance(), f.wallet.Version()
	if _, err := tx.Process(f.input(nil, false)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
	if !f.wallet.Balance().Equal(balance) || f.wallet.Version() != version {
		t.Fatal("re-processing a terminal transaction moved the wallet")
	}
}

func TestProcessRejectsOpening(t *testing.T) {
	f := newFixture(t, "100.00")
	opening, err := NewOpening(newID(t), f.wallet.ID(), f.wallet.PlayerID(), brl(t, "1.00"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opening.Process(f.input(nil, false)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
}
```

- [ ] **Step 2: Rodar os testes e confirmar que falham**

Run: `go test -tags=unit ./internal/domain/wagering/...`
Expected: FAIL de compilação, `undefined: DefaultRetryPolicy`.

- [ ] **Step 3: Implementar**

`internal/domain/wagering/retry.go`:

```go
package wagering

import (
	"errors"
	"time"
)

// RetryPolicy controls how a PENDING_REFERENCE operation waits for its
// reference: exponential backoff (BaseDelay × 2^(n-1), capped at MaxDelay)
// and rejection once MaxAttempts resolutions failed.
type RetryPolicy struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
}

// DefaultRetryPolicy is 2s base, 5min cap, 10 attempts.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{BaseDelay: 2 * time.Second, MaxDelay: 5 * time.Minute, MaxAttempts: 10}
}

// Validate reports an unusable policy.
func (p RetryPolicy) Validate() error {
	switch {
	case p.BaseDelay <= 0:
		return errors.New("wagering: retry base delay must be positive")
	case p.MaxDelay < p.BaseDelay:
		return errors.New("wagering: retry max delay must be >= base delay")
	case p.MaxAttempts < 1:
		return errors.New("wagering: retry max attempts must be >= 1")
	}
	return nil
}

// Delay returns the wait after the given number of failed attempts (>= 1).
func (p RetryPolicy) Delay(attempts int) time.Duration {
	d := p.BaseDelay
	for i := 1; i < attempts; i++ {
		if d >= p.MaxDelay/2 {
			return p.MaxDelay
		}
		d *= 2
	}
	return min(d, p.MaxDelay)
}
```

`internal/domain/wagering/process.go`:

```go
package wagering

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// Reference is the snapshot of a referenced transaction, resolved by the
// caller through (providerId, referenceExternalTransactionId).
type Reference struct {
	ID       uuid.UUID
	Kind     Kind
	Status   Status
	WalletID uuid.UUID
	PlayerID uuid.UUID
	RoundID  string
	Money    money.Money
}

// AsReference returns t as a Reference for operations that point to it.
func (t *WagerTransaction) AsReference() Reference {
	return Reference{
		ID:       t.id,
		Kind:     t.kind,
		Status:   t.status,
		WalletID: t.walletID,
		PlayerID: t.playerID,
		RoundID:  t.roundID,
		Money:    t.money,
	}
}

// ProcessInput is what Process needs from the caller, which must hold the
// wallet row lock inside the SQL transaction.
type ProcessInput struct {
	// Wallet is the locked wallet, or nil when it does not exist.
	Wallet *wallet.Wallet
	// Reference is the resolved reference, or nil when absent or not found.
	Reference *Reference
	// ReferenceAlreadyReversed is true when the reference already has a
	// PROCESSED reversal of the same kind or, for a BET, any PROCESSED
	// REFUND or ROLLBACK.
	ReferenceAlreadyReversed bool
	// LedgerEntryID identifies the ledger entry if the balance moves.
	LedgerEntryID uuid.UUID
	RetryPolicy   RetryPolicy
	Now           time.Time
}

// Process applies the business rules of BET, WIN, LOSS, REFUND and ROLLBACK.
// It moves the wallet balance when the operation succeeds and leaves t
// PROCESSED, REJECTED (with a failure code) or PENDING_REFERENCE. It returns
// the ledger entry when the balance moved. A returned error is never a
// business rejection: it signals misuse or an arithmetic failure.
func (t *WagerTransaction) Process(in ProcessInput) (*wallet.LedgerEntry, error) {
	if t.origin != OriginExternal {
		return nil, fmt.Errorf("%w: only external operations are processed", ErrInvalidTransition)
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return nil, fmt.Errorf("%w: cannot process a %s transaction", ErrInvalidTransition, t.status)
	}
	now := in.Now.UTC()
	w := in.Wallet
	switch {
	case w == nil:
		return nil, t.reject(FailureWalletNotFound, now)
	case w.ID() != t.walletID:
		return nil, fmt.Errorf("%w: wallet %s given for transaction on %s", ErrInvalidTransaction, w.ID(), t.walletID)
	case w.PlayerID() != t.playerID:
		return nil, t.reject(FailureWalletPlayerMismatch, now)
	case w.Currency() != t.money.Currency():
		return nil, t.reject(FailureCurrencyMismatch, now)
	}

	if t.needsReference() {
		code, wait := t.resolveReference(in.Reference, in.ReferenceAlreadyReversed)
		if wait {
			return nil, t.waitForReference(in.RetryPolicy, in.Reference != nil, now)
		}
		if code != "" {
			return nil, t.reject(code, now)
		}
	}

	dir, moves := t.movement()
	if !moves {
		return nil, t.complete(w, nil, now)
	}
	var entry wallet.LedgerEntry
	var err error
	if dir == wallet.DirectionDebit {
		entry, err = w.Debit(t.id, in.LedgerEntryID, t.money, now)
	} else {
		entry, err = w.Credit(t.id, in.LedgerEntryID, t.money, now)
	}
	if errors.Is(err, wallet.ErrInsufficientFunds) {
		if t.kind == KindBet {
			return nil, t.reject(FailureInsufficientFunds, now)
		}
		return nil, t.reject(FailureInsufficientFundsForReversal, now)
	}
	if err != nil {
		return nil, err
	}
	return &entry, t.complete(w, &entry, now)
}

func (t *WagerTransaction) needsReference() bool {
	return t.kind.IsReversal() || (t.kind == KindWin && t.referenceExternalTransactionID != "")
}

// resolveReference checks ref and records it on t when it exists and is
// final. It returns wait=true while the reference is missing or pending, or a
// failure code when the operation must be rejected.
func (t *WagerTransaction) resolveReference(ref *Reference, alreadyReversed bool) (FailureCode, bool) {
	if ref == nil {
		return "", true
	}
	switch ref.Status {
	case StatusPending, StatusPendingReference:
		return "", true
	case StatusRejected, StatusFailed:
		return FailureReferenceNotProcessed, false
	}
	t.referenceTransactionID = ref.ID
	t.referenceKind = ref.Kind
	if !t.acceptsReferenceKind(ref.Kind) {
		return FailureReferenceKindInvalid, false
	}
	if ref.WalletID != t.walletID || ref.PlayerID != t.playerID || ref.RoundID != t.roundID ||
		ref.Money.Currency() != t.money.Currency() {
		return FailureReferenceMismatch, false
	}
	if t.kind.IsReversal() {
		if !ref.Money.Equal(t.money) {
			return FailureAmountMismatch, false
		}
		if alreadyReversed {
			return FailureReferenceAlreadyReversed, false
		}
	}
	return "", false
}

func (t *WagerTransaction) acceptsReferenceKind(k Kind) bool {
	switch t.kind {
	case KindWin, KindRefund:
		return k == KindBet
	case KindRollback:
		return k == KindBet || k == KindWin || k == KindRefund
	default:
		return false
	}
}

// waitForReference counts a failed resolution and schedules the next one, or
// rejects when attempts are exhausted. Only the first wait records
// WagerTransactionPendingReference.
func (t *WagerTransaction) waitForReference(policy RetryPolicy, referenceExists bool, now time.Time) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	t.attempts++
	if t.attempts >= policy.MaxAttempts {
		if referenceExists {
			return t.reject(FailureReferenceNotProcessed, now)
		}
		return t.reject(FailureReferenceNotFound, now)
	}
	t.nextAttemptAt = now.Add(policy.Delay(t.attempts))
	if t.status == StatusPendingReference {
		t.updatedAt = now
		return nil
	}
	if err := t.transition(StatusPendingReference, now); err != nil {
		return err
	}
	t.record(events.WagerTransactionPendingReference{
		TransactionID:                  t.id,
		WalletID:                       t.walletID,
		ProviderID:                     t.providerID,
		ExternalTransactionID:          t.externalTransactionID,
		Kind:                           string(t.kind),
		ReferenceExternalTransactionID: t.referenceExternalTransactionID,
		Attempts:                       t.attempts,
		NextAttemptAt:                  events.FormatTime(t.nextAttemptAt),
	})
	return nil
}

// movement returns the ledger direction of t, or moves=false for LOSS.
func (t *WagerTransaction) movement() (wallet.Direction, bool) {
	switch t.kind {
	case KindBet:
		return wallet.DirectionDebit, true
	case KindWin, KindRefund:
		return wallet.DirectionCredit, true
	case KindRollback:
		if t.referenceKind == KindBet {
			return wallet.DirectionCredit, true
		}
		return wallet.DirectionDebit, true
	default:
		return "", false
	}
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -tags=unit ./internal/domain/...`
Expected: `ok` para os quatro pacotes.

- [ ] **Step 5: Verificação final do plano**

Run:

```bash
gofmt -l . && go vet ./... && go test -race ./... && go test -race -tags=unit ./...
```

Expected: `gofmt -l` sem saída, `go vet` limpo e os dois `go test` com `ok` em `money`, `wallet`, `events` e `wagering`.
Confirmar também que o domínio não importa infraestrutura:

```bash
go list -f '{{join .Imports "\n"}}' ./internal/domain/... | sort -u
```

Expected: só pacotes da stdlib, `github.com/google/uuid` e `github.com/brunopstephan/backend-challenge-go/internal/domain/...`.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/wagering
git commit -m "feat(wagering): business rules for BET/WIN/LOSS/REFUND/ROLLBACK and reference retry" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


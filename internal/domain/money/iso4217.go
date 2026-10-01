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

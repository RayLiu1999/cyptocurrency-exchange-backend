package api

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

// 允許的最大單筆下單數量與金額上限（防止異常超大值）
var (
	maxQuantity   = decimal.NewFromInt(1_000_000)
	maxPrice      = decimal.NewFromInt(100_000_000)
	symbolPattern = regexp.MustCompile(`^[A-Z]{2,10}-[A-Z]{2,10}$`)
)

// validatePlaceOrderRequest 驗證下單請求的合法性
// 在 Gin binding 後呼叫，用於數值範圍與業務邏輯校驗
func validatePlaceOrderRequest(req *placeOrderRequest) error {
	// 驗證 Symbol 格式（必須為 BASE-QUOTE 形式，如 BTC-USD）
	symbol := strings.ToUpper(req.Symbol)
	if !symbolPattern.MatchString(symbol) {
		return fmt.Errorf("交易對格式無效，必須為 BASE-QUOTE 格式 (如 BTC-USD)")
	}

	// 驗證數量：必須大於 0 且不超過上限
	if req.Quantity.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("下單數量必須大於 0")
	}
	if req.Quantity.GreaterThan(maxQuantity) {
		return fmt.Errorf("下單數量超出上限 (最大 %s)", maxQuantity.String())
	}
	// 驗證數量精度不超過 8 位小數
	if req.Quantity.Exponent() < -8 {
		return fmt.Errorf("下單數量精度過高，最多支援 8 位小數")
	}

	// 驗證限價單的價格
	if req.Type == "LIMIT" {
		if req.Price.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("限價單的掛單價格必須大於 0")
		}
		if req.Price.GreaterThan(maxPrice) {
			return fmt.Errorf("掛單價格超出上限 (最大 %s)", maxPrice.String())
		}
		if req.Price.Exponent() < -8 {
			return fmt.Errorf("掛單價格精度過高，最多支援 8 位小數")
		}
	}

	return nil
}

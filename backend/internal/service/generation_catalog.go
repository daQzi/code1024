package service

import (
	"context"

	"github.com/shopspring/decimal"
)

type GenerationCatalogAccount struct {
	GroupID  int64
	Platform string
	Models   []string
	Mapping  map[string]string
	Rules    map[string]GenerationPriceRule
}
type GenerationCatalogRepository interface {
	ListGenerationCatalog(context.Context) ([]GenerationCatalogAccount, error)
}

// Only the sale price is public; upstream credentials, costs and markup stay
// private. More than one account may offer different prices for a model.
type GenerationDisplayPrice struct {
	Dynamic    bool            `json:"dynamic,omitempty"`
	Unit       string          `json:"unit"`
	UnitPrice  decimal.Decimal `json:"unit_price"`
	ServiceFee decimal.Decimal `json:"service_fee"`
	Minimum    decimal.Decimal `json:"minimum"`
	Resolution string          `json:"resolution,omitempty"`
}

func generationDisplayPrice(rule GenerationPriceRule) (GenerationDisplayPrice, bool) {
	if rule.Validate() != nil {
		return GenerationDisplayPrice{}, false
	}
	price := GenerationDisplayPrice{Unit: rule.Unit, ServiceFee: rule.ServiceFee, Minimum: rule.Minimum}
	if rule.Mode == "fixed" {
		price.UnitPrice = *rule.UnitPrice
	} else {
		if rule.SourceUnit == "upstream_cost" {
			price.Unit = "upstream_cost"
			price.Dynamic = true
			return price, true // Dynamic, never presented as a zero unit price.
		}
		if rule.SourceUnitPrice == nil {
			return price, false
		}
		price.Unit = rule.SourceUnit
		price.UnitPrice = rule.SourceUnitPrice.Mul(*rule.ExchangeRate).Mul(*rule.Markup)
	}
	return price, true
}

func (s *ModelPlazaService) appendGenerationCatalog(ctx context.Context, groups map[int64]*PlazaGroup, entities map[int64]*Group) error {
	if s.generationCatalog == nil {
		return nil
	}
	accounts, err := s.generationCatalog.ListGenerationCatalog(ctx)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		group := groups[account.GroupID]
		if group == nil || group.Platform != PlatformComposite && group.Platform != account.Platform {
			continue
		}
		mapping := make(map[string]any, len(account.Mapping))
		for name, target := range account.Mapping {
			mapping[name] = target
		}
		projected := &Account{Platform: account.Platform, Credentials: map[string]any{NewAPIPluginRevisionKey: 1, newAPIPluginVideoModelsKey: account.Models, "model_mapping": mapping}}
		models := projected.NewAPIVideoModels()
		for _, model := range models {
			if !entities[account.GroupID].ModelAllowlist.Allows(model) {
				continue
			}
			at := -1
			for i := range group.Models {
				if group.Models[i].Name == model && group.Models[i].Platform == account.Platform {
					at = i
					break
				}
			}
			if at < 0 {
				group.Models = append(group.Models, PlazaModel{Name: model, Platform: account.Platform})
				at = len(group.Models) - 1
			}
			item := &group.Models[at]
			item.Protocols = []string{"openai_video"}
			rule, ok := account.Rules[model]
			if !ok {
				rule, ok = account.Rules["*"]
			}
			var prices []GenerationDisplayPrice
			if ok {
				if price, valid := generationDisplayPrice(rule); valid {
					prices = append(prices, price)
				}
			} else {
				for _, resolution := range []string{"480p", "720p", "1080p"} {
					if configured := entities[account.GroupID].GetVideoPriceForModel(model, resolution); configured != nil {
						prices = append(prices, GenerationDisplayPrice{Unit: "second", UnitPrice: decimal.NewFromFloat(*configured), Resolution: resolution})
					}
				}
			}
			for _, price := range prices {
				duplicate := false
				for _, existing := range item.GenerationPricing {
					if existing.Unit == price.Unit && existing.UnitPrice.Equal(price.UnitPrice) && existing.ServiceFee.Equal(price.ServiceFee) && existing.Minimum.Equal(price.Minimum) && existing.Resolution == price.Resolution {
						duplicate = true
						break
					}
				}
				if !duplicate && len(item.GenerationPricing) < 64 {
					item.GenerationPricing = append(item.GenerationPricing, price)
				}
			}
		}
	}
	return nil
}

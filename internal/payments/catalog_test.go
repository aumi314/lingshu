package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPancakeCatalogAndExplicitResourceCreation(t *testing.T) {
	c, _ := configAndOrder("waffo_pancake")
	_, _, _, private := rsaFixture(t)
	c.Secrets["private_key"] = private
	c.Values = map[string]string{"merchant_id": "MER_AbCdEfGhIjKlMnOpQrStUv"}
	const store = "STO_AbCdEfGhIjKlMnOpQrStUv"
	const product = "PROD_AbCdEfGhIjKlMnOpQrStUv"
	g := NewGateway()
	calls := []string{}
	failedPublish := false
	g.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		var in map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&in)
		switch {
		case r.URL.Path == "/v1/graphql":
			if !strings.Contains(string(in["query"]), "onetimeProducts") {
				t.Error("product catalog missing")
			}
			return response(`{"data":{"stores":[{"id":"` + store + `","name":"Lingshu","prodEnabled":true,"onetimeProducts":[{"id":"` + product + `","name":"Wallet","status":"active"}]}]}}`), nil
		case strings.HasSuffix(r.URL.Path, "create-store"):
			return response(`{"data":{"store":{"id":"` + store + `"}}}`), nil
		case strings.HasSuffix(r.URL.Path, "create-product"):
			if !strings.Contains(string(in["prices"]), "saas") || string(in["storeId"]) != `"`+store+`"` {
				t.Error("wrong product setup")
			}
			return response(`{"data":{"product":{"id":"` + product + `"}}}`), nil
		case strings.HasSuffix(r.URL.Path, "publish-product"):
			if failedPublish {
				return response(`{"errors":[{"message":"not published"}]}`), nil
			}
			return response(`{"data":{"product":{"id":"` + product + `"}}}`), nil
		default:
			t.Errorf("unexpected SDK endpoint %s", r.URL.Path)
			return response(`{}`), nil
		}
	})
	ctx := context.Background()
	catalog, err := g.PancakeCatalog(ctx, c)
	if err != nil || len(catalog.Stores) != 1 || len(catalog.Stores[0].Products) != 1 {
		t.Fatal("catalog", err)
	}
	if len(calls) != 1 {
		t.Fatal("reading catalog created remote resources")
	}
	result, err := g.CreatePancakeProduct(ctx, c, "https://gateway.example")
	if err != nil || result.StoreID != store || result.ProductID != product {
		t.Fatal("create", err)
	}
	c.Values["store_id"] = store
	failedPublish = true
	calls = nil
	result, err = g.CreatePancakeProduct(ctx, c, "https://gateway.example")
	if err == nil || result.StoreID != store || result.ProductID != product {
		t.Fatal("partial creation IDs lost")
	}
	for _, path := range calls {
		if strings.HasSuffix(path, "create-store") {
			t.Fatal("existing store duplicated")
		}
	}
}

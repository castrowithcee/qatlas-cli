package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	articleID      = "1e3d2c4b-5a6f-4789-9abc-def012345678"
	otherArticleID = "2e3d2c4b-5a6f-4789-9abc-def012345678"
)

const articleBody = `{"id":"` + articleID + `","title":"Beratung","description":"` + bodyCanary + `",
  "type":"SERVICE","articleNumber":"A-100","gtin":"4006381333931","note":"intern","unitName":"Stunde",
  "price":{"netPrice":84.034482758620689,"grossPrice":99.9,"leadingPrice":"NET","taxRate":19},
  "archived":false,"version":2}`

const articlesPageBody = `{"content":[` + articleBody + `,{"id":"` + otherArticleID + `","title":"Leer",
  "type":"PRODUCT","archived":true,"version":1}],"last":false,"totalPages":9,"totalElements":210,
  "size":25,"number":1}`

func TestListArticlesSendsExactlyTheControlledQuery(t *testing.T) {
	tests := []struct {
		name    string
		options ArticleListOptions
		want    url.Values
	}{
		{"defaults", ArticleListOptions{}, url.Values{"page": {"0"}, "size": {"25"}}},
		{"all filters", ArticleListOptions{ArticleNumber: "A_%-1", GTIN: "4006381333931", Type: "PRODUCT", Page: 4, Size: 10},
			url.Values{"page": {"4"}, "size": {"10"}, "articleNumber": {"A_%-1"}, "gtin": {"4006381333931"}, "type": {"PRODUCT"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve(t, func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.Path != "/v1/articles" {
					t.Errorf("request = %s %s", request.Method, request.URL.Redacted())
				}
				if got := request.URL.Query(); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("query = %v, want %v", got, tt.want)
				}
				return jsonResponse(http.StatusOK, articlesPageBody), nil
			})
			c, _ := client(t)
			if _, err := c.ListArticles(context.Background(), tt.options); err != nil {
				t.Fatalf("ListArticles() = %v", err)
			}
		})
	}
}

func TestListArticlesNormalizesPageAndPrice(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, articlesPageBody), nil
	})
	c, _ := client(t)
	result, err := c.ListArticles(context.Background(), ArticleListOptions{})
	if err != nil {
		t.Fatalf("ListArticles() = %v", err)
	}
	if result.Page != 1 || result.Size != 25 || result.TotalPages != 9 || result.TotalElements != 210 ||
		result.LastPage || len(result.Articles) != 2 {
		t.Fatalf("result = %+v", result)
	}
	article := result.Articles[0]
	if article.ID != articleID || article.Title != "Beratung" || article.Type != "SERVICE" ||
		article.ArticleNumber != "A-100" || article.GTIN != "4006381333931" || article.UnitName != "Stunde" ||
		article.Version != 2 || article.Price == nil || article.Price.NetPrice != "84.034482758620689" ||
		article.Price.GrossPrice != "99.9" || article.Price.LeadingPrice != "NET" || article.Price.TaxRate != "19" {
		t.Errorf("article = %+v", article)
	}
	if bare := result.Articles[1]; bare.Price != nil || !bare.Archived {
		t.Errorf("bare = %+v", bare)
	}
	encoded, _ := json.Marshal(article)
	if !strings.Contains(string(encoded), `"net_price":84.034482758620689`) {
		t.Errorf("price lost digits: %s", encoded)
	}
}

func TestArticleArgumentsAreRefusedBeforeIOAndSecretAccess(t *testing.T) {
	refuse(t)
	var reads atomic.Int32
	red := &redact.Redactor{}
	counting := secret.NewWith(func(string) string { reads.Add(1); return primaryKey }, nil, nil, red)
	core := application.New(registry(t), coreConfig(), counting, red)
	for name, request := range map[string]application.InvokeRequest{
		"type":           {Operation: "lexware.articles.list", Arguments: json.RawMessage(`{"type":"GOODS"}`)},
		"gtin":           {Operation: "lexware.articles.list", Arguments: json.RawMessage(`{"gtin":"abc"}`)},
		"free parameter": {Operation: "lexware.articles.list", Arguments: json.RawMessage(`{"archived":true}`)},
		"size":           {Operation: "lexware.articles.list", Arguments: json.RawMessage(`{"size":0}`)},
		"path id":        {Operation: "lexware.articles.get", Arguments: json.RawMessage(`{"id":"../contacts/` + contactID + `"}`)},
	} {
		request.Connection = "lexware-primary"
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s: the core accepted the request", name)
		}
	}
	c, _ := client(t)
	for _, options := range []ArticleListOptions{{Type: "GOODS"}, {GTIN: "12"}, {GTIN: "4006381333x31"}, {Page: 201}} {
		if _, err := c.ListArticles(context.Background(), options); err == nil {
			t.Errorf("ListArticles(%+v) was accepted", options)
		}
	}
	for _, id := range []string{"", "42", articleID + "0"} {
		if _, err := c.GetArticle(context.Background(), id); err == nil {
			t.Errorf("GetArticle(%q) was accepted", id)
		}
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}

func TestGetArticleAndItsFailures(t *testing.T) {
	t.Run("reads the requested article", func(t *testing.T) {
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/articles/"+articleID || request.URL.RawQuery != "" {
				t.Errorf("request = %s", request.URL.Redacted())
			}
			return jsonResponse(http.StatusOK, articleBody), nil
		})
		c, _ := client(t)
		record, err := c.GetArticle(context.Background(), articleID)
		if err != nil || record.ID != articleID || record.Description != bodyCanary {
			t.Fatalf("GetArticle() = %+v, %v", record, err)
		}
	})
	t.Run("another article", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"id":"`+otherArticleID+`"}`), nil
		})
		c, _ := client(t)
		_, err := c.GetArticle(context.Background(), articleID)
		if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), otherArticleID) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
		})
		c, _ := client(t)
		_, err := c.GetArticle(context.Background(), articleID)
		if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(),
			"Lexware does not hold this article or does not show it to this API key") ||
			strings.Contains(err.Error(), articleID) || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("get: %v", err)
		}
		_, err = c.ListArticles(context.Background(), ArticleListOptions{})
		if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), "does not hold") {
			t.Errorf("list: %v", err)
		}
	})
	t.Run("list entry without identifier", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"content":[{"id":"`+bodyCanary+`"}]}`), nil
		})
		c, _ := client(t)
		_, err := c.ListArticles(context.Background(), ArticleListOptions{})
		if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, strings.Repeat("x", maxResponseBytes+1)), nil
		})
		c, _ := client(t)
		if _, err := c.ListArticles(context.Background(), ArticleListOptions{}); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("list: %v", err)
		}
		if _, err := c.GetArticle(context.Background(), articleID); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("get: %v", err)
		}
	})
}

func TestArticleOperationsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/articles" {
			return jsonResponse(http.StatusOK, articlesPageBody), nil
		}
		return jsonResponse(http.StatusOK, articleBody), nil
	})
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)

	list, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.articles.list", Connection: "lexware-primary",
		Arguments: json.RawMessage(`{"type":"SERVICE","article_number":"A-100","size":25}`),
	})
	if err != nil || !strings.Contains(string(list.Result), `"total_elements":210`) ||
		!strings.Contains(string(list.Result), `"last_page":false`) {
		t.Fatalf("invoke list = %s, %v", list.Result, err)
	}
	got, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.articles.get", Connection: "lexware-primary",
		Arguments: json.RawMessage(`{"id":"` + articleID + `"}`),
	})
	if err != nil || !strings.Contains(string(got.Result), `"leading_price":"NET"`) {
		t.Fatalf("invoke get = %s, %v", got.Result, err)
	}
}

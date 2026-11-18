package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/graphql-go/graphql"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/support"
)

// Schema builds the GraphQL schema over the same functions the REST routes use.
func (s *Server) Schema() (graphql.Schema, error) {
	reason := graphql.NewObject(graphql.ObjectConfig{Name: "Reason", Fields: graphql.Fields{
		"source":   &graphql.Field{Type: graphql.String},
		"status":   &graphql.Field{Type: graphql.String},
		"until":    &graphql.Field{Type: graphql.DateTime},
		"event":    &graphql.Field{Type: graphql.String},
		"grants":   &graphql.Field{Type: graphql.Boolean},
		"describe": &graphql.Field{Type: graphql.String},
	}})
	access := graphql.NewObject(graphql.ObjectConfig{Name: "Access", Fields: graphql.Fields{
		"user":    &graphql.Field{Type: graphql.Int},
		"granted": &graphql.Field{Type: graphql.Boolean},
		"at":      &graphql.Field{Type: graphql.DateTime},
		"reasons": &graphql.Field{Type: graphql.NewList(reason)},
	}})
	entry := graphql.NewObject(graphql.ObjectConfig{Name: "HistoryEntry", Fields: graphql.Fields{
		"status":      &graphql.Field{Type: graphql.String},
		"periodStart": &graphql.Field{Type: graphql.DateTime, Resolve: field(func(h historyEntry) any { return h.PeriodStart })},
		"periodEnd":   &graphql.Field{Type: graphql.DateTime, Resolve: field(func(h historyEntry) any { return h.PeriodEnd })},
		"until":       &graphql.Field{Type: graphql.DateTime},
		"source":      &graphql.Field{Type: graphql.String},
		"at":          &graphql.Field{Type: graphql.DateTime},
	}})
	quote := graphql.NewObject(graphql.ObjectConfig{Name: "VATQuote", Fields: graphql.Fields{
		"country":       &graphql.Field{Type: graphql.String},
		"ratePercent":   &graphql.Field{Type: graphql.Float, Resolve: jsonField("rate_percent")},
		"reverseCharge": &graphql.Field{Type: graphql.Boolean, Resolve: jsonField("reverse_charge")},
		"netCents":      &graphql.Field{Type: graphql.Int, Resolve: jsonField("net_cents")},
		"vatCents":      &graphql.Field{Type: graphql.Int, Resolve: jsonField("vat_cents")},
		"grossCents":    &graphql.Field{Type: graphql.Int, Resolve: jsonField("gross_cents")},
		"basis":         &graphql.Field{Type: graphql.String},
	}})
	seat := graphql.NewObject(graphql.ObjectConfig{Name: "Seat", Fields: graphql.Fields{
		"licence": &graphql.Field{Type: graphql.String},
		"until":   &graphql.Field{Type: graphql.DateTime},
	}})

	query := graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: graphql.Fields{
		"access": &graphql.Field{Type: access,
			Args: graphql.FieldConfigArgument{"user": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.Int)}},
			Resolve: func(p graphql.ResolveParams) (any, error) {
				return Explain(p.Context, s.Store, int64(p.Args["user"].(int)), s.Now())
			}},
		"history": &graphql.Field{Type: graphql.NewList(entry),
			Args: graphql.FieldConfigArgument{"subscription": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}},
			Resolve: func(p graphql.ResolveParams) (any, error) {
				recs, err := s.Store.History(p.Context, p.Args["subscription"].(string))
				out := []historyEntry{}
				for _, h := range recs {
					out = append(out, historyEntry{string(h.Sub.Status), h.Sub.PeriodStart, h.Sub.PeriodEnd, h.Until, h.Source, h.At})
				}
				return out, err
			}},
		"vatQuote": &graphql.Field{Type: quote,
			Args: graphql.FieldConfigArgument{
				"plan":        &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				"billing":     &graphql.ArgumentConfig{Type: graphql.String},
				"ipCountry":   &graphql.ArgumentConfig{Type: graphql.String},
				"cardCountry": &graphql.ArgumentConfig{Type: graphql.String},
				"vatId":       &graphql.ArgumentConfig{Type: graphql.String},
			},
			Resolve: func(p graphql.ResolveParams) (any, error) {
				str := func(k string) string { v, _ := p.Args[k].(string); return v }
				return s.Quote(p.Context, QuoteRequest{Plan: str("plan"), Billing: str("billing"), IP: str("ipCountry"),
					Card: str("cardCountry"), VATID: str("vatId")})
			}},
	}})
	mutation := graphql.NewObject(graphql.ObjectConfig{Name: "Mutation", Fields: graphql.Fields{
		"joinCampus": &graphql.Field{Type: seat,
			Args: graphql.FieldConfigArgument{
				"code": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				"user": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.Int)},
			},
			Resolve: func(p graphql.ResolveParams) (any, error) {
				l, err := s.Store.JoinCampus(p.Context, support.NormaliseCode(p.Args["code"].(string)),
					int64(p.Args["user"].(int)), s.Now())
				if err != nil {
					return nil, err
				}
				return map[string]any{"licence": l.Code, "until": l.Ends}, nil
			}},
	}})
	return graphql.NewSchema(graphql.SchemaConfig{Query: query, Mutation: mutation})
}

func field[T any](get func(T) any) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		v, ok := p.Source.(T)
		if !ok {
			return nil, errors.New("unexpected source")
		}
		return get(v), nil
	}
}

// jsonField reads a snake_case field through the value's JSON form (graphql-go matches Go field names otherwise).
func jsonField(name string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		b, err := json.Marshal(p.Source)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		return m[name], nil
	}
}

// GraphQL serves POST /graphql with {"query", "variables"}.
func (s *Server) GraphQL() http.Handler {
	schema, err := s.Schema()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		var in struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		body, rerr := io.ReadAll(io.LimitReader(r.Body, s.MaxBody))
		if rerr != nil || json.Unmarshal(body, &in) != nil || in.Query == "" {
			fail(w, http.StatusBadRequest, errors.New(`body must be {"query": "...", "variables": {...}}`))
			return
		}
		res := graphql.Do(graphql.Params{Schema: schema, RequestString: in.Query, VariableValues: in.Variables,
			Context: r.Context()})
		reply(w, http.StatusOK, res)
	})
}

package resly

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/opentracing/opentracing-go"
)

var (
	// ErrRequestTimeout is returned when request takes too much time
	// to process it.
	ErrRequestTimeout = errors.New("request processing timed out")
)

// textHandler creates an HTTP handler that writes the given string
// and status as a response.
func textHandler(status int, text string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(status)
		rw.Header().Add("Content-Type", "text/plain")
		rw.Write([]byte(text))
	}
}

func parseURL(values url.Values) (gr Request, err error) {
	query := values.Get("query")
	if query == "" {
		return gr, errors.New("request is missing mandatory 'query' URL parameter")
	}

	var (
		vars    map[string]interface{}
		varsRaw = values.Get("variables")
	)

	if varsRaw != "" {
		if err = json.Unmarshal([]byte(varsRaw), &vars); err != nil {
			return gr, err
		}
	}

	return Request{
		Query:         query,
		Variables:     vars,
		OperationName: values.Get("operationName"),
	}, nil
}

func parseForm(r *http.Request) (gr Request, err error) {
	if err := r.ParseForm(); err != nil {
		return gr, err
	}
	return parseURL(r.PostForm)
}

func parseGraphQL(r *http.Request) (gr Request, err error) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return gr, err
	}
	return Request{Query: string(body)}, nil
}

// parseBody parses GraphQL request from the request JSON body.
func parseBody(r *http.Request) (gr Request, err error) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return gr, err
	}
	err = json.Unmarshal(body, &gr)
	return gr, err
}

type Request struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables"`
	OperationName string                 `json:"operationName"`
}

// ParseRequest parses HTTP request and returns GraphQL request instance
// that contains all required parameters.
//
// This function supports requests provided as part of URL parameters,
// within body as pure GraphQL request, within body as JSON request, and
// as part of form request.
func ParseRequest(r *http.Request) (gr Request, err error) {
	// Parse URL only when request is submitted with "GET" verb.
	if r.Method == http.MethodGet {
		return parseURL(r.URL.Query())
	}
	if r.Method != http.MethodPost {
		return gr, errors.New("POST or GET verb is expected")
	}
	// For server requests body is always non-nil, but client request
	// can be passed here as well.
	if r.Body == nil {
		return gr, errors.New("empty body for POST request")
	}

	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return gr, err
	}

	switch contentType {
	case "application/graphql":
		return parseGraphQL(r)
	case "application/x-www-form-urlencoded":
		return parseForm(r)
	default:
		return parseBody(r)
	}
}

// Server is a handler used to serve GraphQL requests.
type Server struct {
	// Name of the server. Will be used to emit metrics about resolvers.
	Name string

	// RequestTimeout is the maximum duration for handling the entire
	// request. When set to 0, request processing takes as much time
	// as needed.
	//
	// Default is no timeout.
	RequestTimeout time.Duration

	// Tracer specifies an optional tracing implementation that is called
	// when resolver of Type, Query, or Mutation is called during the
	// processing of incoming request.
	Tracer opentracing.Tracer

	Types     []TypeDef
	Queries   []FuncDef
	Mutations []FuncDef
}

// HandleType adds given type definitions in the list of the types.
func (s *Server) HandleType(typedef ...TypeDef) *Server {
	s.Types = append(s.Types, typedef...)
	return s
}

// HandleQuery adds given function definition in the list of queries.
func (s *Server) HandleQuery(name string, fn interface{}) *Server {
	s.Queries = append(s.Queries, NewFunc(name, fn))
	return s
}

// HandleMutation adds given function definition in the list of mutations.
//
// Note, that GraphQL does not allow to use the same type as input and as
// an output within a single mutation. Therefore a common practice is to
// define input types as mutation input.
func (s *Server) HandleMutation(name string, fn interface{}) *Server {
	s.Mutations = append(s.Mutations, NewFunc(name, fn))
	return s
}

// CreateSchema returns compiled GraphQL schema from type and function
// definitions.
func (s *Server) CreateSchema() (schema graphql.Schema, err error) {
	var graphql GraphQL

	tracer := s.Tracer
	if tracer == nil {
		tracer = opentracing.NoopTracer{}
	}

	tracingClosure := DefineTracingFunc(tracer)
	metricsClosure := DefineMetricsFunc(s.Name)

	enclose := func(funcdef FuncDef) FuncDef {
		return EncloseFunc(funcdef, metricsClosure, tracingClosure)
	}

	// Register all defined types and functions within a GraphQL compiler.
	for _, typedef := range s.Types {
		var (
			typedef = typedef
			funcs   = make(map[string]FuncDef, len(typedef.Funcs))
		)
		for name, funcdef := range typedef.Funcs {
			funcs[name] = enclose(funcdef)
		}
		typedef.Funcs = funcs

		if err = graphql.AddType(typedef); err != nil {
			return schema, err
		}
	}
	for _, funcdef := range s.Queries {
		if err = graphql.AddQuery(enclose(funcdef)); err != nil {
			return schema, err
		}
	}
	for _, funcdef := range s.Mutations {
		if err = graphql.AddMutation(enclose(funcdef)); err != nil {
			return schema, err
		}
	}
	return graphql.CreateSchema()
}

// GraphQLHandler returns a new HTTP handler that attempts to parse GraphQL
// request from URL, body, or form and executes request using the specifies
// schema.
//
// On failed request parsing and execution method writes plain error message
// as a response.
func GraphQLHandler(schema graphql.Schema) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		acceptHeader := r.Header.Get("Accept")
		if _, ok := r.URL.Query()["raw"]; !ok && strings.Contains(acceptHeader, "text/html") {
			handlePlayground(rw, r)
			return
		}

		gr, err := ParseRequest(r)
		if err != nil {
			h := textHandler(http.StatusBadRequest, err.Error())
			h.ServeHTTP(rw, r)
			return
		}

		params := graphql.Params{
			Schema:         schema,
			RequestString:  gr.Query,
			VariableValues: gr.Variables,
			OperationName:  gr.OperationName,
			Context:        r.Context(),
		}

		b, err := json.Marshal(graphql.Do(params))
		if err != nil {
			h := textHandler(http.StatusInternalServerError, err.Error())
			h.ServeHTTP(rw, r)
			return
		}

		rw.WriteHeader(http.StatusOK)
		rw.Write(b)
	}
}

// HandleHTTP creates a new HTTP handler used to process GraphQL requests.
//
// This function registers all type and function definitions in the GraphQL
// schema. Produced schema will be used to resolve requests.
//
// On duplicate types, queries or mutations, function panics.
func (s *Server) HandleHTTP() http.Handler {
	// There is no reason to create a server that always returns errors.
	schema, err := s.CreateSchema()
	if err != nil {
		panic(err)
	}

	var handler http.Handler = GraphQLHandler(schema)
	if s.RequestTimeout != 0 {
		handler = http.TimeoutHandler(handler, s.RequestTimeout, ErrRequestTimeout.Error())
	}
	if s.Tracer != nil {
		handler = TracingHandler(handler, s.Tracer)
	}
	return handler
}

package resly

import (
	"html/template"
	"net/http"
)

const playgroundVersion = "1.8.10"

const playgroundTemplate = `
{{ define "index" }}
<!--
The request to this GraphQL server provided the header "Accept: text/html"
and as a result has been presented Playground - an in-browser IDE for
exploring GraphQL.

If you wish to receive JSON, provide the header "Accept: application/json" or
add "&raw" to the end of the URL within a browser.
-->
<!DOCTYPE html>
<html>

<head>
	<meta charset=utf-8/>
	<meta name="viewport" content="user-scalable=no, initial-scale=1.0, minimum-scale=1.0, maximum-scale=1.0, minimal-ui">
	<title>GraphQL Playground</title>
	<link rel="stylesheet" href="//cdn.jsdelivr.net/npm/graphql-playground-react/build/static/css/index.css" />
	<link rel="shortcut icon" href="//cdn.jsdelivr.net/npm/graphql-playground-react/build/favicon.png" />
	<script src="//cdn.jsdelivr.net/npm/graphql-playground-react/build/static/js/middleware.js"></script>
</head>

<body>
	<div id="root">
		<style>
			body {
				background-color: rgb(23, 42, 58);
				font-family: Open Sans, sans-serif;
				height: 90vh;
			}
			#root {
				height: 100%;
				width: 100%;
				display: flex;
				align-items: center;
				justify-content: center;
			}
			.loading {
				font-size: 32px;
				font-weight: 200;
				color: rgba(255, 255, 255, .6);
				margin-left: 20px;
			}
			img {
				width: 78px;
				height: 78px;
			}
			.title {
				font-weight: 400;
			}
		</style>
		<img src='//cdn.jsdelivr.net/npm/graphql-playground-react/build/logo.png' alt=''>
		<div class="loading"> Loading
			<span class="title">GraphQL Playground</span>
		</div>
	</div>
	<script>
	window.addEventListener('load', function (event) {
		GraphQLPlayground.init(document.getElementById('root'), {
			settings: {
				"editor.theme": "light",
				"schema.polling.enable": false,
				"schema.disableComments": true
			},
			endpoint: {{ .Endpoint }},
			setTitle: true
		})
	})
	</script>
</body>

</html>
{{ end }}
`

type playgroundData struct {
	Version  string
	Endpoint string
}

func handlePlayground(rw http.ResponseWriter, r *http.Request) {
	t := template.New("playground")
	t, err := t.Parse(playgroundTemplate)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	data := playgroundData{
		Version:  playgroundVersion,
		Endpoint: r.URL.Path,
	}

	err = t.ExecuteTemplate(rw, "index", data)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

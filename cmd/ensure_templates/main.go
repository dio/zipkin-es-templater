package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	l "github.com/tetratelabs/log"

	"github.com/tetratelabs/zipkin-es-templater/pkg/es"
	t "github.com/tetratelabs/zipkin-es-templater/pkg/templater"
)

const (
	templatePath = "/_template/"
)

var (
	log     = l.RegisterScope("default", "Ensure Zipkin ES Index Templates", 0)
	logOpts = l.DefaultOptions()
)

func main() {
	// init our defaults
	var settings = struct {
		t.Config
		host                 string
		disableStrictTraceID bool
		disableSearch        bool
	}{
		Config: t.DefaultConfig(),
		host:   "http://localhost:9200",
	}
	// os env override
	{
		if str := os.Getenv("INDEX_PREFIX"); str != "" {
			settings.IndexPrefix = str
		}
		if str := os.Getenv("INDEX_REPLICAS"); str != "" {
			if i, err := strconv.ParseInt(str, 10, 32); err == nil {
				settings.IndexReplicas = int(i)
			}
		}
		if str := os.Getenv("INDEX_SHARDS"); str != "" {
			if i, err := strconv.ParseInt(str, 10, 32); err == nil {
				settings.IndexShards = int(i)
			}
		}
		if str := os.Getenv("ES_HOST"); str != "" {
			settings.host = str
		}
		if str, found := os.LookupEnv("DISABLE_STRICT_TRACEID"); found {
			str = strings.ToLower(str)
			if str == "1" || str == "yes" || str == "on" {
				settings.disableStrictTraceID = true
			}
		}
		if str, found := os.LookupEnv("DISABLE_SEARCH"); found {
			str = strings.ToLower(str)
			if str == "1" || str == "yes" || str == "on" {
				settings.disableStrictTraceID = true
			}
		}
	}

	// flag handling
	{
		fs := pflag.NewFlagSet("templater settings", pflag.ContinueOnError)
		fs.StringVarP(&settings.IndexPrefix, "prefix", "p",
			settings.IndexPrefix, "index template name prefix")
		fs.IntVarP(&settings.IndexReplicas, "replicas", "r",
			settings.IndexReplicas, "index replica count")
		fs.IntVarP(&settings.IndexShards, "shards", "s", settings.IndexShards,
			"index shard count")
		fs.BoolVar(&settings.disableStrictTraceID, "disable-strict-traceId",
			settings.disableStrictTraceID,
			"disable strict traceID (when migrating between 64-128bit)")
		fs.BoolVar(&settings.disableSearch, "disable-search",
			settings.disableSearch,
			"disable search indexes (if not using Zipkin UI)")
		fs.StringVarP(&settings.host, "host", "H", settings.host,
			"Elasticsearch host URL")

		logOpts.AttachToFlagSet(fs)

		// parse FlagSet and exit on error
		if err := fs.Parse(os.Args[1:]); err != nil {
			if err == pflag.ErrHelp {
				os.Exit(0)
			}
			fmt.Printf("unable to parse settings: %+v\n", err)
			os.Exit(1)
		}

		settings.StrictTraceID = !settings.disableStrictTraceID
		settings.SearchEnabled = !settings.disableSearch
	}

	// initialize the logging subsystem
	if err := l.Configure(logOpts); err != nil {
		fmt.Printf("failed to configure logging: %v\n", err)
		os.Exit(1)
	}

	// create ES client
	log.Debugf("trying to connect to host: %s", settings.host)
	client, err := es.NewClient(&http.Client{}, settings.host)
	if err != nil {
		fmt.Printf("unable to create ES client: %+v\n", err)
		os.Exit(1)
	}
	log.Infof("connected to Elasticsearch version: %g", client.Version())

	// create Template Service
	tplSvc, err := t.New(settings.Config, client.Version())
	if err != nil {
		log.Errorf("%+v", err)
		os.Exit(1)
	}

	// retrieve all Zipkin index templates
	tpls, err := client.GetTemplates(tplSvc.IndexPrefix() + "*")
	if err != nil {
		log.Errorf("unable to get templates: %+v", err)
		os.Exit(1)
	}

	// check for the Zipkin IndexTemplates and insert if not found
	for _, templateType := range []t.IndexTemplateType{
		t.AutoCompleteType, t.SpanType, t.DependencyType,
	} {
		key := tplSvc.IndexTemplateKey(templateType)
		if _, found := tpls[key]; !found {
			log.Infof("%s template %q missing", templateType, key)

			tpl := tplSvc.TemplateByType(templateType)
			if tpl == nil {
				log.Warnf("%s template not supported", templateType)
				continue
			}

			res, err := client.SetIndexTemplate(key, *tpl)
			if err != nil {
				log.Errorf("unable to create %s template: %+v", templateType, err)
				os.Exit(1)
			}

			log.Infof("%s template update: %s", templateType, res)
		} else {
			log.Debugf("%s template found", templateType)
		}
	}

}

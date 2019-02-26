/*
Copyright 2017 EPAM Systems


This file is part of EPAM Report Portal.
https://github.com/reportportal/service-analyzer

Report Portal is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

Report Portal is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with Report Portal.  If not, see <http://www.gnu.org/licenses/>.
*/
package main

import (
	"context"
	"fmt"
	"github.com/go-chi/chi"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/x-cray/logrus-prefixed-formatter"
	"go.uber.org/fx"
	"gopkg.in/reportportal/commons-go.v5/commons"
	"gopkg.in/reportportal/commons-go.v5/conf"
	"gopkg.in/reportportal/commons-go.v5/server"
	"net/http"
	"os"
	"time"
)

var log = logrus.New()

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.Formatter = &prefixed.TextFormatter{
		DisableColors:   true,
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
		ForceFormatting: true,
	}

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.Out = os.Stdout
}

type (
	//AppConfig is the application configuration
	AppConfig struct {
		*conf.ServerConfig
		*SearchConfig
		ESHosts  []string `env:"ES_HOSTS" envDefault:"http://elasticsearch:9200"`
		LogLevel string   `env:"LOGGING_LEVEL" envDefault:"DEBUG"`
		AmqpURL  string   `env:"AMQP_URL" envDefault:"amqp://guest:guest@rabbitmq:5672/"`
		//AmqpURL          string `env:"AMQP_URL" envDefault:"amqp://rabbitmq:rabbitmq@dev.epm-rpp.projects.epam.com:5672"`
		AmqpExchangeName string `env:"AMQP_EXCHANGE_NAME" envDefault:"av.analyzer"`
	}

	//SearchConfig specified details of queries to elastic search
	SearchConfig struct {
		BoostLaunch    float64 `env:"ES_BOOST_LAUNCH" envDefault:"2.0"`
		BoostUniqueID  float64 `env:"ES_BOOST_UNIQUE_ID" envDefault:"2.0"`
		BoostAA        float64 `env:"ES_BOOST_AA" envDefault:"2.0"`
		MinDocFreq     float64 `env:"ES_MIN_DOC_FREQ" envDefault:"7"`
		MinTermFreq    float64 `env:"ES_MIN_TERM_FREQ" envDefault:"1"`
		MinShouldMatch string  `env:"ES_MIN_SHOULD_MATCH" envDefault:"80%"`
	}
)

func main() {
	app := fx.New(
		fx.Logger(log),

		// Provide all the constructors we need, which teaches Fx how we'd like to
		// construct the *log.Logger, http.Handler, and *http.ServeMux types.
		// Remember that constructors are called lazily, so this block doesn't do
		// much on its own.
		fx.Provide(
			newConfig,
			newServer,
			NewRequestHandler,
			newESClient,
			NewAmqpClient,

			newAmpqConnection,
		),
		// Since constructors are called lazily, we need some invocations to
		// kick-start our application. In this case, we'll use Register. Since it
		// depends on an http.Handler and *http.ServeMux, calling it requires Fx
		// to build those types using the constructors above. Since we call
		// NewMux, we also register Lifecycle hooks to start and stop an HTTP
		// server.
		fx.Invoke(initLogger, initRoutes, initAmpq),
	)

	app.Run()
	if nil != app.Err() {
		log.Errorf("Terminated with error: %v", app.Err())
	}
	log.Error(app.Err())
}

func initLogger(cfg *AppConfig) {
	logLevel, err := logrus.ParseLevel(cfg.LogLevel)
	if nil != err {
		log.Warnf("Unknown logging level %s", cfg.LogLevel)
		logLevel = logrus.DebugLevel
	}
	log.SetLevel(logLevel)
}

func newConfig() (*AppConfig, error) {
	defCfg := conf.EmptyConfig()
	cfg := &AppConfig{
		ServerConfig: defCfg,
		SearchConfig: &SearchConfig{},
	}

	return cfg, conf.LoadConfig(cfg)
}

func newServer(lc fx.Lifecycle, cfg *AppConfig) *server.RpServer {
	info := commons.GetBuildInfo()
	info.Name = "Analysis Service"
	srv := server.New(cfg.ServerConfig, info)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go srv.StartServer()
			return nil
		},
	})

	return srv
}

func initRoutes(srv *server.RpServer, c ESClient, h *RequestHandler) {
	srv.AddHealthCheckFunc(func() error {
		if !c.Healthy() {
			return errors.New("ES Cluster is down")
		}
		return nil
	})

	srv.AddHandler(http.MethodPost, "/_index", func(w http.ResponseWriter, rq *http.Request) error {
		return handleHTTPRequest(w, rq, h.IndexLaunches)
	})
	srv.AddHandler(http.MethodPost, "/_analyze", func(w http.ResponseWriter, rq *http.Request) error {
		return handleHTTPRequest(w, rq, h.AnalyzeLogs)
	})

	srv.AddHandler(http.MethodDelete, "/_index/{index_id}", func(w http.ResponseWriter, rq *http.Request) error {
		if id := chi.URLParam(rq, "index_id"); "" != id {
			return handleHTTPRequest(w, rq, h.DeleteIndex(id))
		}
		return server.ToStatusError(http.StatusBadRequest, errors.New("Index ID is incorrect"))
	})
	srv.AddHandler(http.MethodPut, "/_index/delete", cleanIndexHttpHandler(h))
}

func newAmpqConnection(lc fx.Lifecycle, cfg *AppConfig) (*amqp.Connection, error) {
	connection, err := amqp.DialConfig(cfg.AmqpURL, amqp.Config{
		Vhost:     "analyzer",
		Heartbeat: 10 * time.Second,
	})

	if err != nil {
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			log.Warn("Closing AMQP connection")
			return connection.Close()
		},
	})
	log.Info("Connection to AMQP server has been established")
	return connection, err

}
func newESClient(cfg *AppConfig) ESClient {
	return NewClient(cfg.ESHosts, cfg.SearchConfig)
}

func initAmpq(lc fx.Lifecycle, client *AmqpClient, h RequestHandler, cfg *AppConfig) error {

	var qName string
	err := client.DoOnChannel(func(ch *amqp.Channel) error {
		log.Infof("ExchangeName: %s", cfg.AmqpExchangeName)

		err := ch.ExchangeDeclare(
			cfg.AmqpExchangeName, // name
			amqp.ExchangeDirect,  // kind
			false,                // durable
			true,                 // delete when unused
			false,                // internal
			false,                // noWait
			amqp.Table(map[string]interface{}{
				"analyzer":       cfg.AmqpExchangeName,
				"analyzer_index": true,
			}), // arguments
		)
		if err != nil {
			return errors.Wrap(err, "Failed to declare a exchange")
		}
		log.Infof("Exchange '%s' has been declared", cfg.AmqpExchangeName)

		q, err := ch.QueueDeclare(
			"",    // name
			false, // durable
			true,  // delete when unused
			true,  // exclusive
			false, // noWait
			nil,   // arguments
		)
		if err != nil {
			return errors.Wrapf(err, "Failed to declare a queue: %s", q.Name)
		}
		log.Infof("Queue '%s' has been declared", q.Name)
		qName = q.Name

		err = ch.QueueBind(
			q.Name,               // queue name
			"analyze",            // routing key
			cfg.AmqpExchangeName, // exchange
			false,
			nil)
		if err != nil {
			return errors.Wrapf(err, "Failed to bind a queue: %s", q.Name)
		}

		log.Infof("Queue '%s' has been bound", q.Name)
		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "Unable to init AMQP objects: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			cancel()
			return nil
		},
	})

	return client.Receive(ctx, qName, false, true, false, false,
		func(d amqp.Delivery) error {

			//handleAmqpRequest()
			//json.Unmarshal(d.Body)
			fmt.Println(d)
			return nil
		})
}

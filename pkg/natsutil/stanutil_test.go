/*
Copyright 2020 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package natsutil

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"knative.dev/pkg/logging"
)

//import (
//	"os"
//	"testing"
//	"time"
//
//	"github.com/nats-io/nats-streaming-server/server"
//	"go.uber.org/zap"
//	"go.uber.org/zap/zapcore"
//	"knative.dev/pkg/logging"
//	_ "knative.dev/pkg/system/testing"
//)
//
//const (
//	clusterId = "testClusterId"
//	clientId  = "testClient"
//	natssUrl  = "nats://localhost:4222"
//)
//
//var (
//	logger *zap.SugaredLogger
//)
//
//func TestMain(m *testing.M) {
//	logger = setupLogger()
//	defer logger.Sync()
//
//	stanServer, err := startNatss()
//	if err != nil {
//		panic(err)
//	}
//	defer stopNatss(stanServer)
//
//	retCode := m.Run()
//
//	os.Exit(retCode)
//}
//
////Does not compile?!
//func TestConnectPublishClose(t *testing.T) {
//	// connect
//	natssConn, err := Connect(clusterId, clientId, natssUrl, logger)
//	if err != nil {
//		t.Fatalf("Connect failed: %v", err)
//	}
//	defer Close(natssConn, logger)
//	logger.Infof("natssConn: %v", natssConn)
//
//	//publish
//	msg := []byte("testMessage")
//	err = Publish(natssConn, "testTopic", &msg, logger)
//	if err != nil {
//		t.Errorf("Publish failed: %v", err)
//	}
//}
//
//func startNatss() (*server.StanServer, error) {
//	var err error
//	var stanServer *server.StanServer
//	for i := 0; i < 10; i++ {
//		if stanServer, err = server.RunServer(clusterId); err != nil {
//			logger.Errorf("Start NATSS failed: %+v", err)
//			time.Sleep(1 * time.Second)
//		} else {
//			break
//		}
//	}
//	if err != nil {
//		return nil, err
//	}
//	return stanServer, nil
//}
//
//func stopNatss(server *server.StanServer) {
//	server.Shutdown()
//}

func TestConnect(t *testing.T) {
	_, err := Connect("localhost", "my-client", "localhost", setupLogger())
	if err == nil {
		t.Errorf("Connect() expecting err")
		return
	}
}

func newLoggingConfig() *logging.Config {
	lc := &logging.Config{}
	lc.LoggingConfig = `{
		"level": "info",
		"development": false,
		"outputPaths": ["stdout"],
		"errorOutputPaths": ["stderr"],
		"encoding": "json",
		"encoderConfig": {
			"timeKey": "ts",
			"levelKey": "level",
			"nameKey": "logger",
			"callerKey": "caller",
			"messageKey": "msg",
			"stacktraceKey": "stacktrace",
			"lineEnding": "",
			"levelEncoder": "",
			"timeEncoder": "iso8601",
			"durationEncoder": "",
			"callerEncoder": ""
		}
	}`
	lc.LoggingLevel = make(map[string]zapcore.Level)
	return lc
}

func setupLogger() *zap.SugaredLogger {
	logger, _ := logging.NewLoggerFromConfig(newLoggingConfig(), "stanutil_test")
	return logger
}

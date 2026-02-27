/*
Copyright 2024 The Knative Authors

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

package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	_ "knative.dev/pkg/system/testing"
)

type envConfig struct {
	Sink      string `envconfig:"K_SINK" required:"true"`
	Count     int    `envconfig:"COUNT" default:"1"`
	EventType string `envconfig:"EVENT_TYPE" default:"knative.natsbroker.e2etest"`
}

func main() {
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		log.Print("[ERROR] Failed to process env var: ", err)
		os.Exit(1)
	}
	ctx := cloudevents.ContextWithTarget(context.Background(), env.Sink)

	p, err := cloudevents.NewHTTP()
	if err != nil {
		log.Fatal("failed to create protocol: ", err.Error())
	}

	c, err := cloudevents.NewClient(p, cloudevents.WithTimeNow(), cloudevents.WithUUIDs())
	if err != nil {
		log.Fatal("failed to create client, ", err)
	}

	log.Printf("sleeping before sending %d events of type %s", env.Count, env.EventType)
	time.Sleep(10 * time.Second)
	log.Print("done sleeping, sending events now")
	send(cloudevents.ContextWithRetriesExponentialBackoff(ctx, 10*time.Millisecond, 10), c, env.Count, env.EventType)

	log.Print("all events sent, exiting")
	os.Exit(0)
}

func send(ctx context.Context, c cloudevents.Client, count int, eventType string) {
	for i := 0; i < count; i++ {
		e := cloudevents.NewEvent()
		e.SetType(eventType)
		e.SetSource("https://knative.dev/eventing-natss/e2e")
		e.SetExtension("index", i)
		_ = e.SetData(cloudevents.ApplicationJSON, map[string]interface{}{
			"id":      i,
			"message": "Hello, World!",
		})

		// Try to send with retry.
		ctx := cloudevents.ContextWithRetriesExponentialBackoff(ctx, 10*time.Millisecond, 100)

		if result := c.Send(ctx, e); cloudevents.IsUndelivered(result) {
			log.Print("Failed to send: ", result.Error())
		} else if cloudevents.IsACK(result) {
			log.Printf("Sent event %d of type %s", i, eventType)
		} else if cloudevents.IsNACK(result) {
			log.Print("Sent but not accepted: ", result.Error())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/djthorpe/gopi"
	_ "github.com/djthorpe/gopi-hw/sys/lirc"
	_ "github.com/djthorpe/gopi/sys/logger"
	"github.com/eclipse/paho.mqtt.golang"
	"github.com/wtks/A75C4269"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
)

const (
	ClientID = "rpizerow_aircon"
	SubTopic = "/aircon/action"
	PubTopic = "/aircon/state"
)

var (
	MQTTHost        = os.Getenv("MQTT_HOST")
	MQTTUserName    = os.Getenv("MQTT_USERNAME")
	MQTTPassword    = os.Getenv("MQTT_PASSWORD")
	SlackWebhookUrl = os.Getenv("SLACK_WEBHOOK")
)

type Slack struct {
	Username  string `json:"username,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
	Text      string `json:"text,omitempty"`
}

func main() {
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, os.Kill)

	// init mqtt client
	mqttOpt := mqtt.NewClientOptions()
	mqttOpt.AddBroker(MQTTHost)
	mqttOpt.SetUsername(MQTTUserName)
	mqttOpt.SetPassword(MQTTPassword)
	mqttOpt.SetClientID(ClientID)

	client := mqtt.NewClient(mqttOpt)
	defer client.Disconnect(250)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

	config := gopi.NewAppConfig("lirc")

	recv := make(chan mqtt.Message)
	token := client.Subscribe(SubTopic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		recv <- msg
	})
	if token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

	os.Exit(gopi.CommandLineTool(config, func(app *gopi.AppInstance, done chan<- struct{}) error {
		if app.LIRC == nil {
			return errors.New("missing LIRC module")
		}

		for {
			select {
			case <-sigint:
				done <- gopi.DONE
				return nil
			case msg := <-recv:
				c := A75C4269.Controller{}
				if err := json.Unmarshal(msg.Payload(), &c); err != nil {
					app.Logger.Error(err.Error())
					break
				}

				if err := app.LIRC.PulseSend(c.GetRawSignal()); err != nil {
					return err
				}

				if len(SlackWebhookUrl) > 0 {
					go func() {
						err := send(&Slack{
							Username:  "エアコン",
							IconEmoji: ":cyclone:",
							Text:      makeMessage(&c),
						})
						if err != nil {
							app.Logger.Error(err.Error())
						}
					}()
				}

				payload, _ := json.Marshal(c)
				token := client.Publish(PubTopic, 1, true, string(payload))
				if token.Wait() && token.Error() != nil {
					app.Logger.Error(token.Error().Error())
					break
				}
			}
		}

		done <- gopi.DONE
		return nil
	}))
}

func makeMessage(c *A75C4269.Controller) string {
	switch c.Power {
	case A75C4269.PowerOn:
		// オン
		m := ""
		switch c.Mode {
		case A75C4269.ModeCooler:
			m += "冷房, "
		case A75C4269.ModeHeater:
			m += "暖房, "
		case A75C4269.ModeDehumidifier:
			m += "除湿, "
		default:
			m += "???, "
		}
		m += strconv.FormatUint(uint64(c.PresetTemp), 10) + "℃\n風量: "
		switch c.AirVolume {
		case A75C4269.AirVolumeAuto:
			m += "自動, "
		case A75C4269.AirVolumeStill:
			m += "静, "
		case A75C4269.AirVolumePowerful:
			m += "パワフル, "
		default:
			m += strconv.FormatInt(int64(c.AirVolume-1), 10) + ", "
		}
		m += "風向: "
		switch c.WindDirection {
		case A75C4269.WindDirectionAuto:
			m += "自動"
		default:
			m += strconv.FormatInt(int64(c.WindDirection), 10)
		}

		return m
	default:
		// オフ
		return "オフ:sleeping:"
	}
}

func send(payload *Slack) error {
	b, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, SlackWebhookUrl, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	return nil
}

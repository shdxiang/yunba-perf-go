package main

import (
	"os"
	"bufio"
	"log"
	"flag"
	"time"
	"math"
	"strconv"
	"sync"
	"strings"
	"encoding/binary"
	MQTT "github.com/shdxiang/mqtt.go"
)

var msgSent int = 0
var msgRecv int = 0
var wgReg sync.WaitGroup
var wgSub sync.WaitGroup
var wgRecv sync.WaitGroup
var wgWork sync.WaitGroup

var pubTimes [][]int64 // ns
var usedTimes [][]int64 // ms

var minTime int64 =  math.MaxInt64
var maxTime int64 =  math.MinInt64
var lockTime sync.Mutex

var beginTime int64 =  0
var endTime int64 =  0
var beginTimeSet bool = false
var lockTime2 sync.Mutex


func defaultPublishHandler(client *MQTT.MqttClient, msg MQTT.Message) {
	log.Printf("TOPIC: %s\n", msg.Topic())
	log.Printf("MSG: %s\n", msg.Payload())
}

func onMessageReceived(client *MQTT.MqttClient, message MQTT.Message) {
	ns := time.Now().UnixNano()
	data := message.Payload()

	i := binary.LittleEndian.Uint32(data)
	j := binary.LittleEndian.Uint32(data[4:])

	ms := (ns - pubTimes[i][j]) / 1000000
	usedTimes[i][j] += ms

	lockTime.Lock()
	if minTime > ms {
		minTime = ms
	}
	if maxTime < ms {
		maxTime = ms
	}
	lockTime.Unlock()

	lockTime2.Lock()
	endTime = time.Now().UnixNano() / 1000000
	lockTime2.Unlock()

	// log.Printf("recv msg len: %d", len(data))

	// log.Printf("Received message on topic: %s\n", message.Topic())
	// data := message.Payload()
	// l := len(data)
	// if l > 8 {
	// 	l = 8;
	// }
	// log.Printf("Message: %s\n", data[:l])
	msgRecv++
}

func doReg(index int, regFile *os.File, appkey *string, topic *string, qos int, broker *string) {
	deviceId := strconv.Itoa(time.Now().Second()) + strconv.Itoa(index)

	yunbaClient := &MQTT.YunbaClient{*appkey, deviceId}
	regInfo, err := yunbaClient.Reg()
	if err != nil {
		log.Fatal(err)
	}

	if regInfo.ErrCode != 0 {
		log.Fatal("has error:", regInfo.ErrCode)
	}

	line := strings.Join([]string{regInfo.Client, regInfo.UserName, regInfo.Password}, "|")

	log.Printf("line: %s\n", line)
	writer := bufio.NewWriter(regFile)
    _, err = writer.WriteString(line + "\n")
    if err != nil {
    	log.Fatal(err)
    }

    writer.Flush()
	wgReg.Done()
}

func doWork(index int, clientid *string, user *string, pass *string, broker *string, topic *string, qos int, msgLen int, pubEach int, interval int, doPub bool) {
	connOpts := MQTT.NewClientOptions()
	connOpts.AddBroker(*broker)
	connOpts.SetClientId(*clientid)
	connOpts.SetCleanSession(true)
	connOpts.SetProtocolVersion(0x13)

	connOpts.SetUsername(*user)
	connOpts.SetPassword(*pass)

	connOpts.SetDefaultPublishHandler(defaultPublishHandler)

	client := MQTT.NewClient(connOpts)
	_, err := client.Start()
	if err != nil {
		panic(err)
	} else {
		log.Printf("Connected to %s\n", *broker)
	}

	filter, err := MQTT.NewTopicFilter(*topic, byte(qos))
	if err != nil {
		log.Fatal(err)
	}

	// sub
	client.StartSubscription(onMessageReceived, filter)

	msg := make([]byte, msgLen)
	binary.LittleEndian.PutUint32(msg, uint32(index))

	wgSub.Done()
	wgSub.Wait()

	time.Sleep(2 * time.Second)
	
	if doPub {
		if !beginTimeSet {
			lockTime2.Lock()
			if !beginTimeSet {
				beginTimeSet = true
				beginTime = time.Now().UnixNano() / 1000000
			}
			lockTime2.Unlock()
		}

		for i := 0; i < pubEach; i++ {
			binary.LittleEndian.PutUint32(msg[4:], uint32(i))
			pubTimes[index][i] = time.Now().UnixNano()
			<- client.Publish(MQTT.QoS(qos), *topic, msg)
			// log.Printf("Published\n")
			time.Sleep(time.Duration(interval) * time.Millisecond)
		}
	}

	wgRecv.Wait()

	// unsub
	client.EndSubscription(*topic)
	wgWork.Done()
}

func main() {
	appkey := flag.String("appkey", "563c4afef085fc471efdf803", "YunBa appkey")
	topic := flag.String("topic", "topic_test", "Topic to publish the messages on")
	msgLen := flag.Int("msglen", 8, "Length of message to be published, at least 8 bytes for statistics infomation")
	qos := flag.Int("qos", 0, "The QoS to send the messages at")
	broker := flag.String("broker", "tcp://123.56.125.40:1883", "Broker address, default: tcp://123.56.125.40:1883")

	client := flag.Int("client", 1, "Number of clients for registration and subscription")
	pubClient := flag.Int("pubclient", 1, "Number of client for publishing")
	pubEach := flag.Int("pubeach", 1, "How many publish one client do")
	interval := flag.Int("interval", 1000, "Interval of publishes(when [pubeach] > 1), millisecond")

	reg := flag.Bool("reg", false, "Only register and save the infomation")
	file := flag.String("file", "./reg.info", "Register infomation file")
	//retained := flag.Bool("retained", false, "Are the messages sent with the retained flag")
	flag.Parse()

	if *reg == true {
		regFile, err := os.Create(*file)
		if err != nil {
			log.Fatal(err)
		}
		defer regFile.Close()
		for i := 0; i < *client; i++ {
			wgReg.Add(1)
			go doReg(i, regFile, appkey, topic, *qos, broker)
			time.Sleep(10 * time.Millisecond)
		}
		wgReg.Wait()
		// regFile.Sync()
	} else {
		if *msgLen < 8 {
			*msgLen = 8
		}
		pubTimes = make([][]int64, *pubClient)
		for i := range pubTimes {
			pubTimes[i] = make([]int64, *pubEach)
		}
		usedTimes = make([][]int64, *pubClient)
		for i := range usedTimes {
			usedTimes[i] = make([]int64, *pubEach)
		}

		regFile, err := os.Open(*file)
		if err != nil {
			log.Fatal(err)
		}
		defer regFile.Close()
		fileScanner := bufio.NewScanner(regFile)

		wgRecv.Add(1)
		subClient := 0
		for fileScanner.Scan() {
			regInfo := strings.Split(fileScanner.Text(), "|")
			wgSub.Add(1)
			wgWork.Add(1)
			go doWork(subClient, &regInfo[0], &regInfo[1], &regInfo[2], broker, topic, *qos, *msgLen, *pubEach, *interval, subClient < *pubClient)
			time.Sleep(10 * time.Millisecond)
			subClient++
			if subClient >= *client {
				break
			}
		}

		msgNeedRecv := (*pubClient * *pubEach * subClient)

		for {
			time.Sleep(2 * time.Second)
			log.Printf("msgRecv: %d\n", msgRecv)
			if msgRecv == msgNeedRecv {
				break
			}
		}
		wgRecv.Done()
		wgWork.Wait()
		// dur := lastRecv.Sub(beginPub)
		// mill := int64(dur / 1000000)

		pubTotal := *pubEach * *pubClient

		totalTime := int64(0)

		for _, usedTime := range usedTimes {
			for _, ms := range usedTime {
				totalTime += ms
			}
		}
		log.Printf("\n")
		log.Printf("Pub: %d, Sub: %d, Received: %d", pubTotal, subClient, msgRecv)
		log.Printf("Serial: %d ms, Parallel: %d ms, Max: %d ms, Min: %d ms, Avg: %d ms\n", totalTime, endTime - beginTime, maxTime, minTime, totalTime / int64(subClient * *pubEach))
		log.Printf("\n")
	}
}

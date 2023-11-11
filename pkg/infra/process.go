package infra

import (
	"context"
	"fmt"
	"time"

	"strconv"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

var count1 int = 0

var tx_num int = 100

type myargs struct {
	id                 string
	ca                 string
	version            string
	serialNumber       string
	signature          string
	signatureAlgorithm string
	issure             string

	createTime         string
	endTime            string
	entityName         string
	entityIdentifier   string
	publicKey          string
	publicKeyAlgorithm string
	optionalString     string
}

var args123 = []myargs{
	{
		id:                 "id",
		ca:                 "ca",
		version:            "version",
		serialNumber:       "serialNumber",
		signature:          "signature",
		signatureAlgorithm: "signatureAlgorithm",
		issure:             "issure",

		createTime:         "createTime",
		endTime:            "endTime",
		entityName:         "entityName",
		entityIdentifier:   "entityIdentifier",
		publicKey:          "publicKey",
		publicKeyAlgorithm: "publicKeyAlgorithm",
		optionalString:     "optionalString",
	},
}

func GetRandomCase() []myargs {
	cert := make([]myargs, 0, tx_num)
	j := 0
	for i := 100; i < 100+tx_num; i++ {
		j++
		cert[j].id = strconv.Itoa(i)
		cert[j].ca = "CA5"
		cert[j].version = "1.0"
		cert[j].serialNumber = "5"
		cert[j].signature = "58:a9:98:e7:16:52:4c:40:e7:e1:47:92:19:1b:3a:8f:97:6c:7b:b7:b0:cb:20:6d:ad:b5:d3:47:58:d8:e4:f2:3e:32:e9:ef:87:77:e5:54:36:f4:8d:50:8d:07:b4:77:45:ea:9d:a4:33:36:9b:0b:e0:74:58:11:c5:01:7b:4d"
		cert[j].signatureAlgorithm = "md5WithRSAEncryption"
		cert[j].issure = "apple"
		cert[j].createTime = "2021-06-27 08:00:00"
		cert[j].endTime = "2023-06-27 08:00:00"
		cert[j].entityName = "sike"
		cert[j].entityIdentifier = "DCCAb4CAQEwDQYJKoZIhvcNAQEEBQAwgZ4xCzAJBgNVBAYTAlVTMRAwDgYDVQQIEwdNb250YW5hMRAwD"
		cert[j].publicKey = "00:11:22:33:44:81:2f:de:82:3f:f9:ac:c3:86:4a:66:b7:ec:d4:f1:f6:64:21:ff:f5:a2:34:42:d0:38:9f:c6:dd:3b:6e:26:65:6a:54:96:dd:d2:7b:eb:36:a2:ae:7e:2a:9e:7e:56:a5:b6:87:9f:15:c7:18:66:7e:16:77:e2:a7"
		cert[j].publicKeyAlgorithm = "rsaEncryption"
		cert[j].optionalString = "0"
	}
	return cert
}

func Process(configPath string, num int, burst int, rate float64, logger *log.Logger) error {

	config, err := LoadConfig(configPath)
	logger.Debugf("分片数量为:%d", len(config.Shardings)+1)
	if err != nil {
		return err
	}
	crypto, err := config.LoadCrypto()
	if err != nil {
		return err
	}
	raw := make(chan *Elements, burst)
	signed := make([]chan *Elements, len(config.Endorsers))
	processed := make(chan *Elements, burst)
	envs := make(chan *Elements, burst)
	blockCh := make(chan *AddressedBlock, 65535)
	finishCh := make(chan struct{})
	errorCh := make(chan error, burst)
	assembler := &Assembler{Signer: crypto}
	blockCollector, err := NewBlockCollector(config.CommitThreshold, len(config.Committers))
	if err != nil {
		return errors.Wrap(err, "failed to create block collector")
	}
	for i := 0; i < len(config.Endorsers); i++ {
		signed[i] = make(chan *Elements, burst)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		go assembler.StartSigner(ctx, raw, signed, errorCh)
		go assembler.StartIntegrator(ctx, processed, envs, errorCh)
	}

	proposers, err := CreateProposers(config.NumOfConn, config.Endorsers, logger)
	if err != nil {
		return err
	}
	proposers.Start(ctx, signed, processed, config)

	broadcaster, err := CreateBroadcasters(ctx, config.NumOfConn, config.Orderer, logger)
	if err != nil {
		return err
	}
	broadcaster.Start(ctx, envs, errorCh)

	observers, err := CreateObservers(ctx, config.Channel, config.Shardings, config.Committers, crypto, logger)
	if err != nil {
		return err
	}

	start := time.Now()

	go blockCollector.Start(ctx, blockCh, finishCh, num, time.Now(), true)
	go observers.Start(errorCh, blockCh, start)
	go StartCreateProposal(num, burst, rate, config, crypto, raw, errorCh)

	// 额外向不同的分片发送相同的提案
	for i := 0; i < len(config.Shardings); i++ {
		config.Channel = config.Shardings[i]
		go StartCreateProposal(num, burst, rate, config, crypto, raw, errorCh)

	}

	for {
		select {
		case err = <-errorCh:
			return err
		case <-finishCh:
			duration := time.Since(start)
			logger.Infof("Completed processing transactions.")
			fmt.Printf("tx: %d, duration: %+v, tps: %f\n", num*(len(config.Shardings)+1), duration, float64(num)/duration.Seconds())
			return nil
		}
	}
}

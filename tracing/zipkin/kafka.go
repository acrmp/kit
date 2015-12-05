package zipkin

import (
	"github.com/apache/thrift/lib/go/thrift"
	"github.com/go-kit/kit/log"
	"gopkg.in/Shopify/sarama.v1"
)

var KafkaTopic = "zipkin"

// KafkaCollector implements Collector by forwarding spans to a Kafka
// service.
type KafkaCollector struct {
	producer sarama.AsyncProducer
	logger   log.Logger
}

// KafkaOption sets a parameter for the KafkaCollector
type KafkaOption func(s *KafkaCollector)

// KafkaLogger sets the logger used to report errors in the collection
// process. By default, a no-op logger is used, i.e. no errors are logged
// anywhere. It's important to set this option.
func KafkaLogger(logger log.Logger) KafkaOption {
	return func(k *KafkaCollector) { k.logger = logger }
}

// KafkaProducer sets the producer used to produce to kafka.
func KafkaProducer(p sarama.AsyncProducer) KafkaOption {
	return func(c *KafkaCollector) { c.producer = p }
}

// NewKafkaCollector returns a new Kafka-backed Collector. addrs should be a
// slice of TCP endpoints of the form "host:port".
func NewKafkaCollector(addrs []string, options ...KafkaOption) (Collector, error) {
	c := &KafkaCollector{}
	for _, option := range options {
		option(c)
	}
	if c.producer == nil {
		p, err := sarama.NewAsyncProducer(addrs, nil)
		if err != nil {
			return nil, err
		}
		c.producer = p
	}
	if c.logger == nil {
		c.logger = log.NewNopLogger()
	}

	go c.loop()
	return c, nil
}

func (c *KafkaCollector) loop() {
	for {
		select {
		case pe := <-c.producer.Errors():
			c.logger.Log("failed to produce message", "msg", pe.Msg, "err", pe.Err)
		}
	}
}

// Collect implements Collector.
func (c *KafkaCollector) Collect(s *Span) error {
	c.producer.Input() <- &sarama.ProducerMessage{
		Topic: KafkaTopic,
		Key:   nil,
		Value: sarama.ByteEncoder(byteSerialize(s)),
	}
	return nil
}

func byteSerialize(s *Span) []byte {
	t := thrift.NewTMemoryBuffer()
	p := thrift.NewTBinaryProtocolTransport(t)
	if err := s.Encode().Write(p); err != nil {
		panic(err)
	}
	return t.Buffer.Bytes()
}
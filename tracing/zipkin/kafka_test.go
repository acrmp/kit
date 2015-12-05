package zipkin_test

import (
	"errors"
	"github.com/apache/thrift/lib/go/thrift"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/tracing/zipkin"
	"github.com/go-kit/kit/tracing/zipkin/_thrift/gen-go/zipkincore"
	"gopkg.in/Shopify/sarama.v1"
	"testing"
	"time"
)

type fakeProducer struct {
	in    chan *sarama.ProducerMessage
	err   chan *sarama.ProducerError
	kdown bool
}

func (p *fakeProducer) AsyncClose()                               {}
func (p *fakeProducer) Close() error                              { return nil }
func (p *fakeProducer) Input() chan<- *sarama.ProducerMessage     { return p.in }
func (p *fakeProducer) Successes() <-chan *sarama.ProducerMessage { return nil }
func (p *fakeProducer) Errors() <-chan *sarama.ProducerError      { return p.err }

var spans = []*zipkin.Span{
	zipkin.NewSpan("203.0.113.10:1234", "service1", "avg", 123, 456, 0),
	zipkin.NewSpan("203.0.113.10:1234", "service2", "sum", 123, 789, 456),
	zipkin.NewSpan("203.0.113.10:1234", "service2", "div", 123, 101112, 456),
}

func TestKafkaProduce(t *testing.T) {
	p := &fakeProducer{
		make(chan *sarama.ProducerMessage),
		make(chan *sarama.ProducerError),
		false,
	}
	c, err := zipkin.NewKafkaCollector(
		[]string{"192.0.2.10:9092"}, zipkin.KafkaProducer(p),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range spans {
		m := collectSpan(t, c, p, want)
		testMetadata(t, m)
		got := deserializeSpan(t, m.Value)
		testEqual(t, want, got)
	}
}

func TestKafkaErrors(t *testing.T) {
	p := &fakeProducer{
		make(chan *sarama.ProducerMessage),
		make(chan *sarama.ProducerError),
		true,
	}

	errs := make(chan []interface{}, len(spans))
	lg := log.Logger(log.LoggerFunc(func(keyvals ...interface{}) error {
		if len(keyvals) > 0 && keyvals[0] == "failed to produce message" {
			errs <- keyvals
		}
		return nil
	}))
	c, err := zipkin.NewKafkaCollector(
		[]string{"192.0.2.10:9092"},
		zipkin.KafkaProducer(p),
		zipkin.KafkaLogger(lg),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range spans {
		_ = collectSpan(t, c, p, want)
	}

	for i := 0; i < len(spans); i++ {
		select {
		case <-errs:
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("errors not logged. got %d, wanted %d", i, len(spans))
		}
	}
}

func collectSpan(t *testing.T, c zipkin.Collector, p *fakeProducer, s *zipkin.Span) *sarama.ProducerMessage {
	var m *sarama.ProducerMessage
	rcvd := make(chan bool, 1)
	go func() {
		select {
		case m = <-p.in:
			rcvd <- true
			if p.kdown {
				p.err <- &sarama.ProducerError{m, errors.New("kafka is down")}
			}
		case <-time.After(100 * time.Millisecond):
			rcvd <- false
		}
	}()

	if err := c.Collect(s); err != nil {
		t.Errorf("error during collection: %v", err)
	}
	if !<-rcvd {
		t.Fatal("span message was not produced")
	}
	return m
}

func testMetadata(t *testing.T, m *sarama.ProducerMessage) {
	if m.Topic != "zipkin" {
		t.Errorf("produced to topic %q, want %q", m.Topic, "zipkin")
	}
	if m.Key != nil {
		t.Errorf("produced with key %q, want nil", m.Key)
	}
}

func deserializeSpan(t *testing.T, e sarama.Encoder) *zipkincore.Span {
	bytes, err := e.Encode()
	if err != nil {
		t.Errorf("error in encoding: %v", err)
	}
	s := zipkincore.NewSpan()
	mb := thrift.NewTMemoryBufferLen(len(bytes))
	mb.Write(bytes)
	mb.Flush()
	pt := thrift.NewTBinaryProtocolTransport(mb)
	err = s.Read(pt)
	if err != nil {
		t.Errorf("error in decoding: %v", err)
	}
	return s
}

func testEqual(t *testing.T, want *zipkin.Span, got *zipkincore.Span) {
	if got.TraceId != want.TraceID() {
		t.Errorf("trace_id %d, want %d", got.TraceId, want.TraceID())
	}
	if got.Id != want.SpanID() {
		t.Errorf("id %d, want %d", got.Id, want.SpanID())
	}
	if got.ParentId == nil {
		if want.ParentSpanID() != 0 {
			t.Errorf("parent_id %d, want %d", got.ParentId, want.ParentSpanID())
		}
	} else if *got.ParentId != want.ParentSpanID() {
		t.Errorf("parent_id %d, want %d", got.ParentId, want.ParentSpanID())
	}
}
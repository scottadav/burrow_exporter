package burrow_exporter

import (
	"context"

	"sync"
	"time"

	"net/http"

	"strconv"

	log "github.com/Sirupsen/logrus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type BurrowExporter struct {
	client            *BurrowClient
	metricsListenAddr string
	interval          int
	wg                sync.WaitGroup
}

func (be *BurrowExporter) processGroup(cluster, group string) {
	status, err := be.client.ConsumerGroupLag(cluster, group)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Error("error getting status for consumer group. returning.")
		return
	}

	for _, partition := range status.Status.Partitions {
		KafkaConsumerPartitionLag.With(prometheus.Labels{
			"cluster":   status.Status.Cluster,
			"group":     status.Status.Group,
			"topic":     partition.Topic,
			"partition": strconv.Itoa(int(partition.Partition)),
		}).Set(float64(partition.End.Lag))

		KafkaConsumerPartitionCurrentOffset.With(prometheus.Labels{
			"cluster":   status.Status.Cluster,
			"group":     status.Status.Group,
			"topic":     partition.Topic,
			"partition": strconv.Itoa(int(partition.Partition)),
		}).Set(float64(partition.End.Offset))

		KafkaConsumerPartitionMaxOffset.With(prometheus.Labels{
			"cluster":   status.Status.Cluster,
			"group":     status.Status.Group,
			"topic":     partition.Topic,
			"partition": strconv.Itoa(int(partition.Partition)),
		}).Set(float64(partition.End.MaxOffset))
	}

	KafkaConsumerTotalLag.With(prometheus.Labels{
		"cluster": status.Status.Cluster,
		"group":   status.Status.Group,
	}).Set(float64(status.Status.TotalLag))
}

func (be *BurrowExporter) processCluster(cluster string) {
	groups, err := be.client.ListConsumers(cluster)
	if err != nil {
		log.WithFields(log.Fields{
			"err":     err,
			"cluster": cluster,
		}).Error("error listing consumer groups. returning.")
		return
	}

	wg := sync.WaitGroup{}

	for _, group := range groups.ConsumerGroups {
		wg.Add(1)

		go func(g string) {
			defer wg.Done()
			be.processGroup(cluster, g)
		}(group)
	}

	wg.Wait()
}

func (be *BurrowExporter) startPrometheus() {
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(be.metricsListenAddr, nil)
}

func (be *BurrowExporter) Close() {
	be.wg.Wait()
}

func (be *BurrowExporter) Start(ctx context.Context) {
	be.startPrometheus()

	be.wg.Add(1)
	defer be.wg.Done()

	be.mainLoop(ctx)
}

func (be *BurrowExporter) mainLoop(ctx context.Context) {
	timer := time.NewTicker(time.Duration(be.interval) * time.Second)

	for {
		select {
		case <-ctx.Done():
			log.Info("Shutting down exporter.")
			timer.Stop()
			return

		case <-timer.C:
			log.WithField("timestamp", time.Now().UnixNano()).Info("Scraping burrow...")
			clusters, err := be.client.ListClusters()
			if err != nil {
				log.WithFields(log.Fields{
					"err": err,
				}).Error("error listing clusters. Continuing.")
				continue
			}

			wg := sync.WaitGroup{}

			for _, cluster := range clusters.Clusters {
				wg.Add(1)

				go func(c string) {
					defer wg.Done()
					be.processCluster(c)
				}(cluster)
			}

			wg.Wait()

			log.WithField("timestamp", time.Now().UnixNano()).Info("Finished scraping burrow.")
		}
	}
}

func MakeBurrowExporter(burrowUrl string, metricsAddr string, interval int) *BurrowExporter {
	return &BurrowExporter{
		client:            MakeBurrowClient(burrowUrl),
		metricsListenAddr: metricsAddr,
		interval:          interval,
	}
}

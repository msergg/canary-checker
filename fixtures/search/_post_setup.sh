#!/bin/bash

echo "Running kubectl wait for elasticsearch"
kubectl -n canaries wait --for=condition=ready pod -l app=elasticsearch --timeout=5m

echo "Fetching elastic search health";
curl -s "http://elasticsearch.canaries.svc.cluster.local:9200/_cluster/health" -H 'Content-Type: application/json';
curl -s "http://elasticsearch.canaries.svc.cluster.local:9200/_cluster/allocation/explain" -H 'Content-Type: application/json';

echo "Fetching populate-db logs from elasticsearch pod";
kubectl logs -n canaries -l app=elasticsearch -c populate-db

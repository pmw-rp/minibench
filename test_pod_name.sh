#!/bin/bash

echo "Testing POD_NAME topic naming feature"
echo ""

echo "Test 1: Without use-pod-name flag (default behavior)"
echo "Expected: Topics named test-0, test-1, test-2"
echo ""

echo "Test 2: With use-pod-name flag but no POD_NAME env var"
echo "Expected: Topics named test-0, test-1, test-2 (same as default)"
export USE_POD_NAME=true
echo "USE_POD_NAME=true (but POD_NAME not set)"
echo ""

echo "Test 3: With use-pod-name flag and POD_NAME set"
echo "Expected: Topics named test-kafka-producer-0-0, test-kafka-producer-0-1, test-kafka-producer-0-2"
export POD_NAME="kafka-producer-0"
export USE_POD_NAME=true
export TOPIC_PREFIX="test-"
export TOPIC_COUNT=3
echo "POD_NAME=$POD_NAME"
echo "USE_POD_NAME=$USE_POD_NAME"
echo "TOPIC_PREFIX=$TOPIC_PREFIX"
echo "TOPIC_COUNT=$TOPIC_COUNT"
echo ""

echo "To test this with a real Kafka cluster, run:"
echo "POD_NAME=kafka-producer-0 ./minibench -brokers <your-broker> -use-pod-name -topic-count 3"
echo ""
echo "The tool will create topics named:"
echo "  - test-kafka-producer-0-0"
echo "  - test-kafka-producer-0-1"
echo "  - test-kafka-producer-0-2"

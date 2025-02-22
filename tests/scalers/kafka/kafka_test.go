//go:build e2e
// +build e2e

package kafka_test

import (
	"fmt"
	"testing"

	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/kubernetes"

	. "github.com/kedacore/keda/v2/tests/helper"
)

// Load environment variables from .env file
var _ = godotenv.Load("../../.env")

const (
	testName = "kafka-test"
)

var (
	testNamespace          = fmt.Sprintf("%s-ns", testName)
	deploymentName         = fmt.Sprintf("%s-deployment", testName)
	kafkaName              = fmt.Sprintf("%s-kafka", testName)
	kafkaClientName        = fmt.Sprintf("%s-client", testName)
	scaledObjectName       = fmt.Sprintf("%s-so", testName)
	bootstrapServer        = fmt.Sprintf("%s-kafka-bootstrap.%s:9092", kafkaName, testNamespace)
	strimziOperatorVersion = "0.30.0"
	topic1                 = "kafka-topic"
	topic2                 = "kafka-topic2"
	zeroInvalidOffsetTopic = "kafka-topic-zero-invalid-offset"
	oneInvalidOffsetTopic  = "kafka-topic-one-invalid-offset"
	invalidOffsetGroup     = "invalidOffset"
	topicPartitions        = 3
	falseString            = "false"
	trueString             = "true"
)

type templateData struct {
	TestNamespace        string
	DeploymentName       string
	ScaledObjectName     string
	KafkaName            string
	KafkaTopicName       string
	KafkaTopicPartitions int
	KafkaClientName      string
	TopicName            string
	Topic1Name           string
	Topic2Name           string
	BootstrapServer      string
	ResetPolicy          string
	Params               string
	Commit               string
	ScaleToZeroOnInvalid string
}

const (
	singleDeploymentTemplate = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.DeploymentName}}
  namespace: {{.TestNamespace}}
  labels:
    app: {{.DeploymentName}}
spec:
  replicas: 0
  selector:
    matchLabels:
      app: kafka-consumer
  template:
    metadata:
      labels:
        app: kafka-consumer
    spec:
      containers:
      # only recent version of kafka-console-consumer support flag "include"
      # old version's equiv flag will violate language-matters commit hook
      # work around -> create two consumer container joining the same group
      - name: kafka-consumer
        image: confluentinc/cp-kafka:5.2.1
        command:
          - sh
          - -c
          - "kafka-console-consumer --bootstrap-server {{.BootstrapServer}} {{.Params}} --consumer-property enable.auto.commit={{.Commit}}"
`

	multiDeploymentTemplate = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.DeploymentName}}
  namespace: {{.TestNamespace}}
  labels:
    app: {{.DeploymentName}}
spec:
  replicas: 0
  selector:
    matchLabels:
      app: kafka-consumer
  template:
    metadata:
      labels:
        app: kafka-consumer
    spec:
      containers:
      # only recent version of kafka-console-consumer support flag "include"
      # old version's equiv flag will violate language-matters commit hook
      # work around -> create two consumer container joining the same group
      - name: kafka-consumer
        image: confluentinc/cp-kafka:5.2.1
        command:
          - sh
          - -c
          - "kafka-console-consumer --bootstrap-server {{.BootstrapServer}} --topic '{{.Topic1Name}}'  --group multiTopic --from-beginning --consumer-property enable.auto.commit=false"
      - name: kafka-consumer-2
        image: confluentinc/cp-kafka:5.2.1
        command:
          - sh
          - -c
          - "kafka-console-consumer --bootstrap-server {{.BootstrapServer}} --topic '{{.Topic2Name}}' --group multiTopic --from-beginning --consumer-property enable.auto.commit=false"
`

	singleScaledObjectTemplate = `
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: {{.ScaledObjectName}}
  namespace: {{.TestNamespace}}
  labels:
    app: {{.DeploymentName}}
spec:
  scaleTargetRef:
    name: {{.DeploymentName}}
  triggers:
  - type: kafka
    metadata:
      topic: {{.TopicName}}
      bootstrapServers: {{.BootstrapServer}}
      consumerGroup: {{.ResetPolicy}}
      lagThreshold: '1'
      activationLagThreshold: '1'
      offsetResetPolicy: {{.ResetPolicy}}`

	multiScaledObjectTemplate = `
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: {{.ScaledObjectName}}
  namespace: {{.TestNamespace}}
  labels:
    app: {{.DeploymentName}}
spec:
  scaleTargetRef:
    name: {{.DeploymentName}}
  triggers:
  - type: kafka
    metadata:
      bootstrapServers: {{.BootstrapServer}}
      consumerGroup: multiTopic
      lagThreshold: '1'
      offsetResetPolicy: 'latest'`

	invalidOffsetScaledObjectTemplate = `
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: {{.ScaledObjectName}}
  namespace: {{.TestNamespace}}
  labels:
    app: {{.DeploymentName}}
spec:
  scaleTargetRef:
    name: {{.DeploymentName}}
  triggers:
  - type: kafka
    metadata:
      topic: {{.TopicName}}
      bootstrapServers: {{.BootstrapServer}}
      consumerGroup: {{.ResetPolicy}}
      lagThreshold: '1'
      scaleToZeroOnInvalidOffset: '{{.ScaleToZeroOnInvalid}}'
      offsetResetPolicy: 'latest'`

	kafkaClusterTemplate = `apiVersion: kafka.strimzi.io/v1beta2
kind: Kafka
metadata:
  name: {{.KafkaName}}
  namespace: {{.TestNamespace}}
spec:
  kafka:
    version: "3.1.0"
    replicas: 1
    listeners:
      - name: plain
        port: 9092
        type: internal
        tls: false
      - name: tls
        port: 9093
        type: internal
        tls: true
    config:
      offsets.topic.replication.factor: 1
      transaction.state.log.replication.factor: 1
      transaction.state.log.min.isr: 1
      log.message.format.version: "2.5"
    storage:
      type: ephemeral
  zookeeper:
    replicas: 1
    storage:
      type: ephemeral
  entityOperator:
    topicOperator: {}
    userOperator: {}
`

	kafkaTopicTemplate = `apiVersion: kafka.strimzi.io/v1beta2
kind: KafkaTopic
metadata:
  name: {{.KafkaTopicName}}
  namespace: {{.TestNamespace}}
  labels:
    strimzi.io/cluster: {{.KafkaName}}
  namespace: {{.TestNamespace}}
spec:
  partitions: {{.KafkaTopicPartitions}}
  replicas: 1
  config:
    retention.ms: 604800000
    segment.bytes: 1073741824
`
	kafkaClientTemplate = `
apiVersion: v1
kind: Pod
metadata:
  name: {{.KafkaClientName}}
  namespace: {{.TestNamespace}}
spec:
  containers:
  - name: {{.KafkaClientName}}
    image: confluentinc/cp-kafka:5.2.1
    command:
      - sh
      - -c
      - "exec tail -f /dev/null"`
)

func TestScaler(t *testing.T) {
	// setup
	t.Log("--- setting up ---")
	// Create kubernetes resources
	kc := GetKubernetesClient(t)
	data, templates := getTemplateData()
	CreateKubernetesResources(t, kc, testNamespace, data, templates)
	installKafkaOperator(t)
	addCluster(t, data)
	addTopic(t, data, topic1, topicPartitions)
	addTopic(t, data, topic2, topicPartitions)
	addTopic(t, data, zeroInvalidOffsetTopic, 1)
	addTopic(t, data, oneInvalidOffsetTopic, 1)

	// test scaling
	testEarliestPolicy(t, kc, data)
	testLatestPolicy(t, kc, data)
	testMultiTopic(t, kc, data)
	testZeroOnInvalidOffset(t, kc, data)
	testOneOnInvalidOffset(t, kc, data)

	// cleanup
	uninstallKafkaOperator(t)
	DeleteKubernetesResources(t, kc, testNamespace, data, templates)
}

func testEarliestPolicy(t *testing.T, kc *kubernetes.Clientset, data templateData) {
	t.Log("--- testing earliest policy: scale up ---")
	data.Params = fmt.Sprintf("--topic %s --group earliest --from-beginning", topic1)
	data.Commit = falseString
	data.TopicName = topic1
	data.ResetPolicy = "earliest"
	KubectlApplyWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlApplyWithTemplate(t, data, "singleScaledObjectTemplate", singleScaledObjectTemplate)

	// Shouldn't scale pods applying earliest policy
	AssertReplicaCountNotChangeDuringTimePeriod(t, kc, deploymentName, testNamespace, 0, 60)

	// Shouldn't scale pods with only 1 message due to activation value
	publishMessage(t, topic1)
	AssertReplicaCountNotChangeDuringTimePeriod(t, kc, deploymentName, testNamespace, 0, 60)

	// Scale application with kafka messages
	publishMessage(t, topic1)
	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, 2, 60, 2),
		"replica count should be %d after 2 minute", 2)

	// Scale application beyond partition max.
	messages := 5
	for i := 0; i < messages; i++ {
		publishMessage(t, topic1)
	}

	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, topicPartitions, 60, 2),
		"replica count should be %d after 2 minute", messages)

	KubectlDeleteWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlDeleteWithTemplate(t, data, "singleScaledObjectTemplate", singleScaledObjectTemplate)
}

func testLatestPolicy(t *testing.T, kc *kubernetes.Clientset, data templateData) {
	t.Log("--- testing latest policy: scale up ---")
	commitPartition(t, topic1, "latest")
	data.Params = fmt.Sprintf("--topic %s --group latest", topic1)
	data.Commit = falseString
	data.TopicName = topic1
	data.ResetPolicy = "latest"
	KubectlApplyWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlApplyWithTemplate(t, data, "singleScaledObjectTemplate", singleScaledObjectTemplate)

	// Shouldn't scale pods
	AssertReplicaCountNotChangeDuringTimePeriod(t, kc, deploymentName, testNamespace, 0, 60)

	// Shouldn't scale pods with only 1 message due to activation value
	publishMessage(t, topic1)
	AssertReplicaCountNotChangeDuringTimePeriod(t, kc, deploymentName, testNamespace, 0, 60)

	// Scale application with kafka messages
	publishMessage(t, topic1)
	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, 2, 60, 2),
		"replica count should be %d after 2 minute", 2)

	// Scale application beyond partition max.
	messages := 5
	for i := 0; i < messages; i++ {
		publishMessage(t, topic1)
	}

	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, topicPartitions, 60, 2),
		"replica count should be %d after 2 minute", messages)

	KubectlDeleteWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlDeleteWithTemplate(t, data, "singleScaledObjectTemplate", singleScaledObjectTemplate)
}

func testMultiTopic(t *testing.T, kc *kubernetes.Clientset, data templateData) {
	t.Log("--- testing multi topic: scale up ---")
	commitPartition(t, topic1, "multiTopic")
	commitPartition(t, topic2, "multiTopic")
	data.Topic1Name = topic1
	data.Topic2Name = topic2
	KubectlApplyWithTemplate(t, data, "multiDeploymentTemplate", multiDeploymentTemplate)
	KubectlApplyWithTemplate(t, data, "multiScaledObjectTemplate", multiScaledObjectTemplate)

	// Shouldn't scale pods
	AssertReplicaCountNotChangeDuringTimePeriod(t, kc, deploymentName, testNamespace, 0, 60)

	// Scale application with kafka messages in topic 1
	publishMessage(t, topic1)
	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, 1, 60, 2),
		"replica count should be %d after 2 minute", 1)

	// Scale application with kafka messages in topic 2
	// // produce one more msg to the different topic within the same group
	// // will turn total consumer group lag to 2.
	// // with lagThreshold as 1 -> making hpa AverageValue to 1
	// // this should turn nb of replicas to 2
	// // as desiredReplicaCount = totalLag / avgThreshold
	publishMessage(t, topic2)
	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, 2, 60, 2),
		"replica count should be %d after 2 minute", 2)

	KubectlDeleteWithTemplate(t, data, "multiDeploymentTemplate", multiDeploymentTemplate)
	KubectlDeleteWithTemplate(t, data, "multiScaledObjectTemplate", multiScaledObjectTemplate)
}

func testZeroOnInvalidOffset(t *testing.T, kc *kubernetes.Clientset, data templateData) {
	t.Log("--- testing zeroInvalidOffsetTopic: scale up ---")
	data.Params = fmt.Sprintf("--topic %s --group %s", zeroInvalidOffsetTopic, invalidOffsetGroup)
	data.Commit = trueString
	data.TopicName = zeroInvalidOffsetTopic
	data.ResetPolicy = invalidOffsetGroup
	data.ScaleToZeroOnInvalid = trueString
	KubectlApplyWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlApplyWithTemplate(t, data, "invalidOffsetScaledObjectTemplate", invalidOffsetScaledObjectTemplate)

	// Shouldn't scale pods
	AssertReplicaCountNotChangeDuringTimePeriod(t, kc, deploymentName, testNamespace, 0, 60)

	KubectlDeleteWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlDeleteWithTemplate(t, data, "invalidOffsetScaledObjectTemplate", invalidOffsetScaledObjectTemplate)
}

func testOneOnInvalidOffset(t *testing.T, kc *kubernetes.Clientset, data templateData) {
	t.Log("--- testing oneInvalidOffsetTopic: scale up ---")
	data.Params = fmt.Sprintf("--topic %s --group %s --from-beginning", oneInvalidOffsetTopic, invalidOffsetGroup)
	data.Commit = trueString
	data.TopicName = oneInvalidOffsetTopic
	data.ResetPolicy = invalidOffsetGroup
	data.ScaleToZeroOnInvalid = falseString
	KubectlApplyWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlApplyWithTemplate(t, data, "invalidOffsetScaledObjectTemplate", invalidOffsetScaledObjectTemplate)

	// Should scale to 1
	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, 1, 60, 2),
		"replica count should be %d after 2 minute", 1)

	commitPartition(t, oneInvalidOffsetTopic, invalidOffsetGroup)
	publishMessage(t, oneInvalidOffsetTopic)

	// Should scale to 0
	assert.True(t, WaitForDeploymentReplicaReadyCount(t, kc, deploymentName, testNamespace, 0, 60, 10),
		"replica count should be %d after 10 minute", 0)

	KubectlDeleteWithTemplate(t, data, "singleDeploymentTemplate", singleDeploymentTemplate)
	KubectlDeleteWithTemplate(t, data, "invalidOffsetScaledObjectTemplate", invalidOffsetScaledObjectTemplate)
}

func publishMessage(t *testing.T, topic string) {
	_, _, err := ExecCommandOnSpecificPod(t, kafkaClientName, testNamespace, fmt.Sprintf(`echo "{"text": "foo"}" | kafka-console-producer --broker-list %s --topic %s`, bootstrapServer, topic))
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
}

func commitPartition(t *testing.T, topic string, group string) {
	_, _, err := ExecCommandOnSpecificPod(t, kafkaClientName, testNamespace, fmt.Sprintf(`kafka-console-consumer --bootstrap-server %s --topic %s --group %s --from-beginning --consumer-property enable.auto.commit=true --timeout-ms 15000`, bootstrapServer, topic, group))
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
}

func installKafkaOperator(t *testing.T) {
	_, err := ExecuteCommand("helm repo add strimzi https://strimzi.io/charts/")
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
	_, err = ExecuteCommand("helm repo update")
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
	_, err = ExecuteCommand(fmt.Sprintf(`helm upgrade --install --namespace %s --wait %s strimzi/strimzi-kafka-operator --version %s`,
		testNamespace,
		testName,
		strimziOperatorVersion))
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
}

func uninstallKafkaOperator(t *testing.T) {
	_, err := ExecuteCommand(fmt.Sprintf(`helm uninstall --namespace %s %s`,
		testNamespace,
		testName))
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
}

func addTopic(t *testing.T, data templateData, name string, partitions int) {
	data.KafkaTopicName = name
	data.KafkaTopicPartitions = partitions
	KubectlApplyWithTemplate(t, data, "kafkaTopicTemplate", kafkaTopicTemplate)
	_, err := ExecuteCommand(fmt.Sprintf("kubectl wait kafkatopic/%s --for=condition=Ready --timeout=300s --namespace %s", name, testNamespace))
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
}

func addCluster(t *testing.T, data templateData) {
	KubectlApplyWithTemplate(t, data, "kafkaClusterTemplate", kafkaClusterTemplate)
	_, err := ExecuteCommand(fmt.Sprintf("kubectl wait kafka/%s --for=condition=Ready --timeout=300s --namespace %s", kafkaName, testNamespace))
	assert.NoErrorf(t, err, "cannot execute command - %s", err)
}

func getTemplateData() (templateData, []Template) {
	return templateData{
			TestNamespace:    testNamespace,
			DeploymentName:   deploymentName,
			KafkaName:        kafkaName,
			KafkaClientName:  kafkaClientName,
			BootstrapServer:  bootstrapServer,
			TopicName:        topic1,
			Topic1Name:       topic1,
			Topic2Name:       topic2,
			ResetPolicy:      "",
			ScaledObjectName: scaledObjectName,
		}, []Template{
			{Name: "kafkaClientTemplate", Config: kafkaClientTemplate},
		}
}

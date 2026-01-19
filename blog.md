# 从零开始：构建一个自定义的 OpenTelemetry Collector 和 Processor

最近在做企业级数据看板需求时，遇到了一个有意思的问题：如何在 metrics 数据流经 OpenTelemetry Collector 时做聚合处理，并且保证进程重启后状态不丢失？这促使我深入研究了 OTel Collector 的扩展机制。这篇文章记录了整个实践过程。

## 背景与需求

我们的业务场景是这样的：上游有大量应用不断产生细粒度的 metrics，比如每次 API 调用都会带一个唯一的 `work.id`。这些数据直接推送到时序数据库会产生高基数问题（high cardinality），既浪费存储也影响查询性能。

我们希望在 Collector 这一层做预聚合——按 `work.type` 维度汇总，丢弃高基数的 `work.id`，每隔固定时间向下游输出聚合后的结果。这个需求用现成的 processor 都不太好实现，所以决定自己写一个。

## 系统架构总览

先说说整体的数据流向。在我们的场景中，数据从应用端出发，经过 Collector 处理后，最终落到可观测性后端：

```
┌───────────────────────────────────┐
│          Python App               │
│  ┌─────────────────────────────┐  │
│  │  counter.add() / gauge.set()│  │
│  └──────────────┬──────────────┘  │
│                 ▼                 │
│  ┌─────────────────────────────┐  │
│  │    Memory Queue (异步)       │  │
│  │    [可选: 持久化到磁盘]       │  │
│  └──────────────┬──────────────┘  │
│                 ▼                 │
│  ┌─────────────────────────────┐  │
│  │  OTLP Exporter (批量发送)    │  │
│  └──────────────┬──────────────┘  │
└─────────────────┼─────────────────┘
                  │ OTLP/gRPC
                  ▼
┌─────────────────────────────────────────────────┐
│              OTel Collector                     │
│  ┌───────────────────────────────────────────┐  │
│  │ OTLP Receiver                             │  │
│  └─────────────────────┬─────────────────────┘  │
│                        ▼                        │
│  ┌───────────────────────────────────────────┐  │
│  │ Simple Processor                          │  │
│  │ (聚合 + 状态持久化 → Redis)                 │  │
│  └─────────────────────┬─────────────────────┘  │
│                        ▼                        │
│  ┌───────────────────────────────────────────┐  │
│  │ Exporter + Sending Queue                  │  │
│  │ [可选: 持久化队列]                          │  │
│  └─────────────────────┬─────────────────────┘  │
└────────────────────────┼────────────────────────┘
                         │ Pull (:9464) 或 Push (Remote Write)
                         ▼
          ┌─────────────────────────────┐
          │   Prometheus / Grafana      │
          └─────────────────────────────┘
```

这里有几个关键的设计决策。Receiver 选用 OTLP 协议，因为它是 OTel 的原生格式，SDK 都原生支持。Exporter 这边比较有意思，我同时配了 Prometheus exporter，后面会详细聊 push 和 pull 的选择问题。

## 开发环境搭建

OTel Collector 的扩展开发依赖一个叫 `ocb`（OpenTelemetry Collector Builder）的工具。它的作用是根据配置文件生成一个定制化的 Collector 发行版，里面只包含你需要的组件。

首先准备构建配置 `builder-config.yaml`：

```yaml
dist:
  name: otelcol-dev
  output_path: ./otelcol-dev

exporters:
  - gomod: go.opentelemetry.io/collector/exporter/debugexporter v0.140.0
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/exporter/prometheusexporter v0.140.0

processors:
  - gomod: github.com/myuser/simpleprocessor v0.0.1
    path: ./myprocessor  # 指向本地代码

receivers:
  - gomod: go.opentelemetry.io/collector/receiver/otlpreceiver v0.140.0

extensions:
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/extension/storage/redisstorageextension v0.140.1
```

注意 processor 那部分：我们的自定义 processor 通过 `path` 字段指向本地目录，这样 ocb 在构建时会引用本地代码而不是从远程拉取。

运行 `./ocb --config builder-config.yaml`，它会在 `otelcol-dev/` 目录下生成完整的 Go 代码和可执行文件。

## Processor 的核心实现

一个 processor 需要实现两个东西：Factory（工厂函数）和 Processor（实际逻辑）。

Factory 负责告诉 Collector「我叫什么名字」以及「怎么创建我的实例」。Config 结构体定义用户可以在 YAML 里配置的参数，这里我们支持两种持久化方式：写本地文件，或者用 Redis。

```go
func NewFactory() processor.Factory {
    // 注册 processor 类型名为 "simple"，绑定配置和创建函数
}

type Config struct {
    CheckpointFile string        `mapstructure:"checkpoint_file"`
    StorageID      *component.ID `mapstructure:"storage"`
}
```

Processor 主体实现 `ConsumeMetrics` 方法，Collector pipeline 会把接收到的 metrics 传进来：

```go
type simpleProcessor struct {
    next         consumer.Metrics  // 下游消费者
    aggregations map[string]int64  // 按 work.type 聚合的状态
    // ...
}

func (p *simpleProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
    // 遍历所有 metric data points
    // 按 work.type 维度聚合，忽略高基数的 work.id
    // 数据暂存内存，不立即调用 next.ConsumeMetrics
    // 由后台 flush 协程定期输出聚合结果
}

func (p *simpleProcessor) flushLoop() {
    // 每 5 秒触发一次 flush
    // 构造聚合后的 metrics，发送到下游
    // 同时持久化当前状态
}
```

这段代码有个很重要的设计：我们没有立即调用 `p.next.ConsumeMetrics()`，而是把数据攒在内存里。这实现了「批处理」语义——输入的细粒度数据被聚合后，由一个后台协程定期 flush 到下游。

我们用了 Cumulative 时间语义，输出的是「从进程启动到现在的累计值」，而不是「这 5 秒内的增量」。这个选择和持久化策略紧密相关。

## 容错性设计

一条 metrics 数据从产生到落盘，中间经过三个节点：客户端应用、Collector、下游存储。任何一个环节都可能出问题，我们需要分别考虑应对策略。

**上游挂了（客户端应用崩溃或网络抖动）**

这其实是最「无害」的情况。OTel SDK 内置了 retry 机制，临时的网络问题会自动重试。如果客户端进程直接挂了，那段时间的 metrics 确实会丢失，但这通常是可接受的——毕竟进程都挂了，没有 metrics 也合理。

更值得关注的是客户端发送过快导致 Collector 处理不过来的情况。这就引出了客户端队列的设计。

OTel SDK 的 exporter 架构本身就是基于队列的。业务代码调用 `counter.add()` 时，数据并不会立即发送，而是先进入一个内存队列，由后台线程批量导出。这个设计同时解决了两个问题：性能上，业务线程不会被网络 IO 阻塞，发送操作是完全异步的；吞吐上，小数据攒成大批次发送，减少网络往返。

Python SDK 的 `PeriodicExportingMetricReader` 就是这个模式的体现——它每隔固定间隔（比如 5 秒）把累积的 metrics 批量推给 exporter。

但内存队列有个问题：进程崩溃时队列里的数据就丢了。如果你的业务对数据完整性要求很高，可以考虑持久化队列。思路是在数据进入队列时先写一份到本地磁盘（SQLite、RocksDB 或者简单的 append-only 文件都行），发送成功后再删除。这样即使进程意外退出，重启后也能从磁盘恢复未发送的数据。

当然，持久化队列会带来额外的写盘开销。实际中需要权衡：对于 metrics 这种高频、可容忍少量丢失的数据，内存队列通常就够了；但如果是计费相关的 metrics，或者审计日志，持久化就很有必要。

**Collector 挂了（进程崩溃或重启）**

这是最棘手的情况，因为 Collector 是有状态的——我们的 processor 在内存里维护着聚合数据。

如果用的是 Cumulative 语义，Collector 重启后计数器会从 0 开始。下游 Prometheus 看到的数据会突然「跳回」一个小值，基于 `rate()` 的告警规则会产生错误的 spike。

解决方案是状态持久化。我们支持两种方式：

本地文件最简单，适合单节点部署。Redis Storage Extension 更适合生产环境，OTel Contrib 提供了 `redisstorageextension`，processor 通过 storage API 读写数据。

```go
func (p *simpleProcessor) Start(ctx context.Context, host component.Host) error {
    // 从 host 获取 storage extension
    // 初始化 storage client
    // 加载之前持久化的状态
    // 启动后台 flush 协程
}

func (p *simpleProcessor) Shutdown(ctx context.Context) error {
    // 停止 flush 协程
    // 最后一次持久化状态
    // 关闭 storage client
}
```

用 Redis 还有个好处：如果你跑多个 Collector 副本做高可用，它们可以共享状态。当然，这时候需要小心并发问题，简单的 map 操作可能需要换成 Redis 的原子操作。

**下游挂了（Prometheus 或存储后端不可用）**

这个问题在 push 和 pull 模式下表现不同。

Pull 模式（Prometheus Exporter）天然免疫这个问题。Collector 只是暴露一个 `/metrics` 端点，Prometheus 来不来抓是它的事。抓取失败了，Prometheus 下次再来就是。Collector 这边的数据一直在内存里，不会丢。

Push 模式（OTLP Exporter / Remote Write）就需要额外处理了。如果 `next.ConsumeMetrics` 返回错误，当前实现只是 log 一下，数据就丢了。更健壮的做法是实现 retry with backoff，或者启用 exporter 的 sending queue 功能：

```yaml
exporters:
  prometheusremotewrite:
    endpoint: http://cortex:9009/api/v1/push
    sending_queue:
      enabled: true
      num_consumers: 10
      queue_size: 1000
      storage: file_storage  # 持久化队列，重启不丢
    retry_on_failure:
      enabled: true
      initial_interval: 5s
      max_interval: 30s
```

这样即使下游暂时不可用，数据也会在本地排队，等下游恢复后继续发送。

## 性能考量

在高吞吐场景下，几个地方需要注意。

锁的粒度是个问题。我们用了一把大锁保护整个 `aggregations` map，这在写入频率极高时可能成为瓶颈。一个优化方向是分片——按 work.type 的 hash 值分成多个子 map，各自持有独立的锁。

Flush 频率需要权衡。Flush 太频繁，下游压力大；Flush 太稀疏，数据延迟高。5 秒是个折中值，实际要根据业务 SLA 调整。

内存占用也要关注。如果 work.type 的基数很高，aggregations map 会持续膨胀。可能需要加个上限，或者定期清理过期的 key。

## 自定义配置源
在实际生产中，我们可能已经有现成的配置管理系统，例如 `etcd` 或者 `spring-cloud`，因此如果能把 otel 也接入到这些系统，就会非常方便。ocb 支持在 builder 中定义 provider 模块，而我们只需要实现里面的 `Provider` 接口。其本质就是从一个 uri 读取数据，解析成配置结构体。

```go
Retrieve(ctx context.Context, uri string, watcher WatcherFunc)
```

除此之外，`watcher` 参数也让我们能够实现配置的热更新。


## Push vs Pull：Exporter 的选择

最后聊聊 metrics 输出的两种模式。

**Push 模式（OTLP Exporter / Prometheus Remote Write）**

Collector 主动把数据推到远端。好处是延迟可控，配置简单，不需要暴露额外端口。坏处是对接收端有压力，如果推送速度超过了处理速度，要么丢数据要么本地排队。

Remote Write 是 Prometheus 生态里常用的 push 方式，协议基于 Protobuf，效率不错。适合往 Cortex、Thanos、VictoriaMetrics 这类分布式时序库写数据。

**Pull 模式（Prometheus Exporter）**

Collector 暴露一个 HTTP 端点（比如 `:9464/metrics`），等 Prometheus server 来抓。这是 Prometheus 的「正统」玩法，好处是天然有背压——抓取方控制频率，不会压垮被抓取方。坏处是需要服务发现机制让 Prometheus 知道去哪抓，在动态环境（比如 K8s Pod 扩缩容）下稍麻烦一些。

我们的配置里同时开了两种：

```yaml
exporters:
  debug:
    verbosity: detailed
  prometheus:
    endpoint: 0.0.0.0:9464

service:
  pipelines:
    metrics:
      exporters: [debug, prometheus]
```

开发阶段用 debug exporter 看原始数据；生产环境用 prometheus exporter 让监控系统来抓。如果你的基础设施更偏向 push（比如用的是 Datadog 或 New Relic），换成对应的 exporter 就行。

## 实际跑起来

启动顺序是：先起 Redis（如果用了 storage extension），再起 Collector，最后跑客户端发数据。

```bash
# 终端 1：起 Redis
cd docker && docker-compose up

# 终端 2：编译并运行 Collector
cd otelcol-dev && go build -o ./build/otelcol-dev
./build/otelcol-dev --config config.yaml

# 终端 3：发送测试数据
cd client-app && uv run main.py
```

Python 客户端会发送 100 条 metrics，每条带有 `work.type=manual` 和唯一的 `work.id`。经过我们的 processor 聚合后，你在 Prometheus（或 debug 输出）里只会看到一条 `work_done_batched{work.type="manual"}` 计数器，值是累加后的总数。

## 小结

写一个自定义的 OTel Processor 没有想象中复杂。核心就是实现 `ConsumeXxx` 方法，在里面做你想做的变换或聚合。ocb 工具让你能很方便地把自定义组件和官方组件组合成一个独立的 Collector 二进制。

生产环境要多考虑几件事：客户端的 retry 策略、Collector 的状态持久化、下游不可用时的 queue 和重试。Push 还是 Pull，取决于你的基础设施偏好。两种都能工作，选适合自己架构的就好。

---

*代码仓库：这篇文章的完整示例代码可以在 [otel-research](https://github.com/xxx/otel-research) 找到，包括自定义 processor、构建配置和测试客户端。*

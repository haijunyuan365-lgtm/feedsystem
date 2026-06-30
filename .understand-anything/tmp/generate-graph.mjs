import { readFileSync, writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { basename, join } from 'node:path';
import { execFileSync } from 'node:child_process';

const projectRoot = process.cwd();
const ua = join(projectRoot, '.understand-anything');
const tmp = join(ua, 'tmp');
const intermediate = join(ua, 'intermediate');
mkdirSync(tmp, { recursive: true });
mkdirSync(intermediate, { recursive: true });

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
}

function writeJson(path, value) {
  writeFileSync(path, JSON.stringify(value, null, 2), 'utf8');
}

function gitCommit() {
  try {
    return execFileSync('git', ['rev-parse', 'HEAD'], { cwd: projectRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
  } catch {
    return 'unknown';
  }
}

function fileNodeId(file) {
  if (file.fileCategory === 'config') return `config:${file.path}`;
  if (file.fileCategory === 'infra') return file.path === '.dockerignore' ? `resource:${file.path}` : `service:${file.path}`;
  if (file.fileCategory === 'docs') return `document:${file.path}`;
  return `file:${file.path}`;
}

function complexity(nonEmptyLines = 0, metrics = {}) {
  const functionCount = Number(metrics.functionCount || 0);
  const classCount = Number(metrics.classCount || 0);
  if (nonEmptyLines > 200 || functionCount > 12 || classCount > 8) return 'complex';
  if (nonEmptyLines >= 50 || functionCount >= 4 || classCount >= 2) return 'moderate';
  return 'simple';
}

function tagsForFile(file) {
  const p = file.path;
  if (p.startsWith('cmd/')) return ['入口点', 'go-binary', '启动流程'];
  if (p.includes('/handler')) return ['api-handler', 'gin', '请求处理'];
  if (p.includes('/service')) return ['service', '业务逻辑', '领域编排'];
  if (p.includes('/repo')) return ['repository', 'gorm', '数据访问'];
  if (p.includes('/entity')) return ['data-model', 'gorm-model', '请求响应'];
  if (p.includes('/middleware/jwt')) return ['middleware', 'jwt', '认证'];
  if (p.includes('/middleware/redis')) return ['redis', 'cache', 'middleware'];
  if (p.includes('/middleware/rabbitmq')) return ['rabbitmq', 'message-queue', 'middleware'];
  if (p.includes('/middleware/ratelimit')) return ['middleware', 'rate-limit', '安全'];
  if (p.includes('/worker/')) return ['worker', '异步处理', '消息消费'];
  if (p.includes('/http/router')) return ['router', 'gin', 'api-routes'];
  if (p.includes('/config/')) return ['configuration', 'yaml', 'runtime-config'];
  if (p.includes('/db/')) return ['database', 'gorm', 'mysql'];
  if (p.includes('/auth/')) return ['jwt', 'auth', 'token'];
  if (p.includes('/observability/')) return ['observability', 'pprof', 'diagnostics'];
  if (p === 'Dockerfile') return ['containerization', 'docker', 'deployment'];
  if (p === 'docker-compose.yml') return ['orchestration', 'docker-compose', 'local-env'];
  if (p.startsWith('configs/')) return ['configuration', 'runtime-config', 'yaml'];
  if (p === 'go.mod' || p === 'go.sum') return ['dependencies', 'go-module', 'build-system'];
  return ['go', 'backend', 'module'];
}

function domainName(path) {
  const m = path.match(/^internal\/([^/]+)\//);
  if (!m) return '';
  const names = {
    account: '账号', feed: 'Feed', video: '视频', social: '社交关系', message: '消息通知',
    middleware: '基础中间件', worker: '异步 worker', http: 'HTTP 路由', config: '配置加载',
    db: '数据库', auth: '认证', apierror: 'API 错误', observability: '可观测性',
  };
  return names[m[1]] || m[1];
}

function summaryForFile(result) {
  const p = result.path;
  const d = domainName(p);
  if (p === 'cmd/main.go') return 'API 服务入口，负责加载环境与配置，初始化 pprof、MySQL、Redis、RabbitMQ，并挂载 Gin 路由启动 HTTP 服务。';
  if (p === 'cmd/worker/main.go') return '后台 worker 入口，初始化配置、数据库、Redis 与 RabbitMQ，并启动通知、点赞、评论、社交关系、Feed timeline 和热度等异步消费者。';
  if (p === 'internal/http/router.go') return '集中注册 Gin 路由、认证中间件、限流中间件和各业务 handler，是外部 HTTP API 进入账号、视频、社交与消息模块的枢纽。';
  if (p === 'internal/config/loadConfig.go') return '从 YAML 与环境变量加载运行时配置，覆盖服务器、数据库、Redis、RabbitMQ 和可观测性参数。';
  if (p === 'internal/db/db.go') return '封装 GORM/MySQL 连接、自动迁移与连接关闭逻辑，为业务仓储层提供数据库基础设施。';
  if (p === 'internal/auth/jwt.go') return '封装 JWT access token 与 refresh token 的生成、解析和校验逻辑，支撑登录态与认证中间件。';
  if (p.includes('/handler')) return `${d} HTTP handler，负责绑定请求、调用 service，并将业务结果转换为 Gin JSON 响应。`;
  if (p.includes('/service')) return `${d} service 层，承载核心业务规则并编排仓储、缓存、消息队列或其他领域模块。`;
  if (p.includes('/repo')) return `${d} repository 层，使用 GORM 封装数据库查询、写入和聚合统计。`;
  if (p.includes('/entity')) return `${d}实体、请求 DTO 与响应 DTO 定义，描述数据库模型和 API 数据结构。`;
  if (p.includes('/middleware/jwt')) return 'Gin JWT 中间件，从请求中解析 token 并把账号身份写入上下文供受保护路由使用。';
  if (p.includes('/middleware/rabbitmq')) return 'RabbitMQ 中间件封装队列、交换机和消息发布细节，为异步事件流提供统一入口。';
  if (p.includes('/middleware/redis')) return 'Redis 中间件封装客户端、缓存键、分布式锁和有序集合操作，支撑缓存、限流和榜单能力。';
  if (p.includes('/middleware/ratelimit')) return '基于 Redis 的限流中间件，为 Gin 路由提供按 key 计数和过期窗口控制。';
  if (p.includes('/worker/')) return `${d}处理器，消费 RabbitMQ 或 Redis 中的异步任务并更新数据库、缓存或 SSE 推送。`;
  if (p.includes('/apierror/')) return '统一 API 错误类型与错误码定义，帮助 handler 层输出稳定的错误响应。';
  if (p.includes('/observability/')) return 'pprof 可观测性服务封装，用于在运行时按配置开启诊断端口。';
  if (p === 'Dockerfile') return '多阶段 Docker 构建文件，分别构建 API 与 worker 二进制并打包进最小运行时镜像。';
  if (p === 'docker-compose.yml') return '本地 Docker Compose 编排，启动应用依赖的 MySQL、Redis、RabbitMQ 以及服务容器。';
  if (p === '.dockerignore') return 'Docker 构建上下文排除规则，避免临时文件、构建产物和本地配置进入镜像构建上下文。';
  if (p.startsWith('configs/')) return '运行环境 YAML 配置，定义服务器端口、数据库、Redis、RabbitMQ 与 pprof 等连接参数。';
  if (p === 'go.mod') return 'Go module 依赖清单，声明 Gin、GORM、MySQL driver、JWT、Redis 与 RabbitMQ 等后端依赖。';
  if (p === 'go.sum') return 'Go module 校验和文件，锁定依赖版本的完整性校验信息。';
  return `${d || '后端'}模块文件，参与 feedsystem 的 Go 服务实现。`;
}

function functionSummary(name, filePath) {
  if (name === 'main' && filePath === 'cmd/main.go') return '组装并启动 HTTP API 服务，按顺序初始化配置、数据库、缓存、消息队列和路由。';
  if (name === 'main' && filePath === 'cmd/worker/main.go') return '组装后台消费者运行时，启动多个异步 worker 并保持进程运行。';
  if (name.startsWith('New')) return `构造 ${name.replace(/^New/, '')} 组件并注入其依赖。`;
  if (name.startsWith('List')) return '查询列表型业务数据，并处理分页、游标或排序条件。';
  if (name.startsWith('Get') || name.startsWith('Find')) return '读取并返回目标业务数据。';
  if (name.startsWith('Create') || name.startsWith('Upload')) return '处理创建或上传类业务流程，并持久化相关状态。';
  if (name.startsWith('Update') || name.startsWith('Rename') || name.startsWith('Change')) return '处理更新类业务流程，并校验请求上下文。';
  if (name.startsWith('Delete') || name.startsWith('Unlike') || name.startsWith('Unfollow')) return '处理删除、取消或撤销类业务动作。';
  if (name.startsWith('Like') || name.startsWith('Follow')) return '处理用户互动动作，并同步必要的计数、缓存或异步事件。';
  if (name.startsWith('Publish') || name.startsWith('Enqueue')) return '发布异步消息或任务，供后台 worker 消费。';
  if (name.startsWith('Start') || name.startsWith('Run')) return '启动长运行处理流程或后台循环。';
  if (/^(parse|build|set|get|rand|list|rebuild)/.test(name)) return `为业务流程提供 ${name} 辅助逻辑。`;
  return `${name} 实现 ${domainName(filePath) || '模块'}中的一段业务逻辑。`;
}

function classSummary(name, filePath) {
  if (name.endsWith('Handler')) return `${name} 聚合 HTTP handler 方法并持有 service 依赖。`;
  if (name.endsWith('Service')) return `${name} 承载 ${domainName(filePath) || '领域'}业务编排能力。`;
  if (name.endsWith('Repo') || name.endsWith('Repository')) return `${name} 封装 ${domainName(filePath) || '领域'}数据访问能力。`;
  if (name === 'Client') return `${name} 封装外部中间件客户端能力。`;
  if (name.endsWith('Request') || name.endsWith('Response')) return `${name} 定义 API 请求或响应数据结构。`;
  return `${name} 是 ${domainName(filePath) || '模块'}中的核心 Go 类型。`;
}

function nodeTypeForResult(result) {
  if (result.fileCategory === 'config') return 'config';
  if (result.fileCategory === 'infra') return result.path === '.dockerignore' ? 'resource' : 'service';
  if (result.fileCategory === 'docs') return 'document';
  return 'file';
}

function isSignificantFunction(fn, exports) {
  return exports.has(fn.name) || (Number(fn.endLine || 0) - Number(fn.startLine || 0) + 1) >= 10;
}

function isSignificantClass(cls, exports) {
  const lines = Number(cls.endLine || 0) - Number(cls.startLine || 0) + 1;
  return exports.has(cls.name) || (cls.methods?.length || 0) >= 2 || lines >= 20;
}

function edge(source, target, type, weight) {
  return { source, target, type, direction: 'forward', weight };
}

function dedupeEdges(edges) {
  const seen = new Set();
  return edges.filter((e) => {
    if (!e.source || !e.target || e.source === e.target) return false;
    const key = `${e.source}|${e.target}|${e.type}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

const scan = readJson(join(intermediate, 'scan-result.json'));
const batches = readJson(join(intermediate, 'batches.json')).batches;
const allFileNodes = new Map(scan.files.map((f) => [f.path, fileNodeId(f)]));

for (const batch of batches) {
  const idx = Number(batch.batchIndex);
  const extract = readJson(join(tmp, `ua-file-extract-results-${idx}.json`));
  const nodes = [];
  const edges = [];
  for (const result of extract.results) {
    const id = allFileNodes.get(result.path) || fileNodeId(result);
    const metrics = result.metrics || {};
    nodes.push({
      id,
      type: nodeTypeForResult(result),
      name: basename(result.path),
      filePath: result.path,
      summary: summaryForFile(result),
      tags: tagsForFile(result),
      complexity: complexity(result.nonEmptyLines || result.totalLines || 0, metrics),
    });

    const exported = new Set((result.exports || []).map((item) => item.name));
    for (const cls of result.classes || []) {
      if (!isSignificantClass(cls, exported)) continue;
      const clsId = `class:${result.path}:${cls.name}`;
      nodes.push({
        id: clsId,
        type: 'class',
        name: cls.name,
        filePath: result.path,
        lineRange: [cls.startLine, cls.endLine],
        summary: classSummary(cls.name, result.path),
        tags: ['go-type', '领域模型', '结构体'],
        complexity: complexity((cls.endLine || 0) - (cls.startLine || 0) + 1, { classCount: 1, functionCount: cls.methods?.length || 0 }),
      });
      edges.push(edge(id, clsId, 'contains', 1.0));
    }

    for (const fn of result.functions || []) {
      if (!isSignificantFunction(fn, exported)) continue;
      const fnId = `function:${result.path}:${fn.name}`;
      nodes.push({
        id: fnId,
        type: 'function',
        name: fn.name,
        filePath: result.path,
        lineRange: [fn.startLine, fn.endLine],
        summary: functionSummary(fn.name, result.path),
        tags: ['go-function', result.path.includes('/handler') ? 'api-handler' : result.path.includes('/service') ? 'service-method' : '业务逻辑'],
        complexity: complexity((fn.endLine || 0) - (fn.startLine || 0) + 1, { functionCount: 1 }),
      });
      edges.push(edge(id, fnId, 'contains', 1.0));
    }

    for (const target of batch.batchImportData?.[result.path] || []) {
      const targetId = allFileNodes.get(target) || `file:${target}`;
      edges.push(edge(id, targetId, 'imports', 0.7));
    }

    if (result.path === 'Dockerfile') {
      for (const stage of result.services || []) {
        const serviceId = `service:${result.path}:${stage.name}`;
        nodes.push({
          id: serviceId,
          type: 'service',
          name: stage.name,
          filePath: result.path,
          lineRange: [stage.startLine, stage.endLine],
          summary: `Dockerfile 的 ${stage.name} 阶段，基于 ${stage.image} 构建应用依赖、源码、二进制或运行时镜像。`,
          tags: ['docker-stage', 'containerization', 'deployment'],
          complexity: 'simple',
        });
        edges.push(edge(id, serviceId, 'contains', 1.0));
      }
      for (const target of ['cmd/main.go', 'cmd/worker/main.go']) edges.push(edge(id, allFileNodes.get(target), 'deploys', 0.7));
      edges.push(edge(id, allFileNodes.get('go.mod'), 'depends_on', 0.6));
    }
    if (result.path === 'docker-compose.yml') {
      for (const target of ['Dockerfile', 'configs/config.compose-local.yaml']) {
        edges.push(edge(id, allFileNodes.get(target), target === 'Dockerfile' ? 'deploys' : 'configures', 0.6));
      }
    }
    if (result.path.startsWith('configs/')) {
      for (const target of ['cmd/main.go', 'cmd/worker/main.go', 'internal/config/loadConfig.go']) edges.push(edge(id, allFileNodes.get(target), 'configures', 0.6));
    }
    if (result.path === 'go.mod') {
      for (const target of ['cmd/main.go', 'cmd/worker/main.go']) edges.push(edge(id, allFileNodes.get(target), 'configures', 0.6));
    }
  }
  writeJson(join(intermediate, `batch-${idx}.json`), { nodes, edges: dedupeEdges(edges) });
  console.log(`batch-${idx}.json nodes=${nodes.length} edges=${dedupeEdges(edges).length}`);
}

const skillDir = 'C:\\Users\\YHJ\\.understand-anything\\repo\\understand-anything-plugin\\skills\\understand';
try {
  const mergeOutput = execFileSync('D:\\\\Software\\\\Anaconda3\\\\python.exe', [join(skillDir, 'merge-batch-graphs.py'), projectRoot], { cwd: projectRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  if (mergeOutput.trim()) console.log(mergeOutput.trim());
} catch (err) {
  process.stderr.write(err.stderr?.toString() || err.message);
  process.exit(1);
}

const assembled = readJson(join(intermediate, 'assembled-graph.json'));
const nodeIds = new Set(assembled.nodes.map((n) => n.id));

function ids(paths) {
  return paths.map((p) => allFileNodes.get(p) || `file:${p}`).filter((id) => nodeIds.has(id));
}

const layerDefs = [
  ['layer:entrypoints', '入口与运行时', 'API 进程、worker 进程以及 HTTP 路由注册，是系统启动和请求进入的位置。', ['cmd/main.go', 'cmd/worker/main.go', 'internal/http/router.go']],
  ['layer:domain-account-social', '账号与社交领域', '账号、关注关系和消息通知相关实体、仓储、服务和 handler。', scan.files.filter((f) => /^internal\/(account|social|message)\//.test(f.path)).map((f) => f.path)],
  ['layer:domain-video-feed', '视频与 Feed 领域', '视频、评论、点赞、标签、Feed 聚合与热度排序相关代码。', scan.files.filter((f) => /^internal\/(video|feed)\//.test(f.path)).map((f) => f.path)],
  ['layer:middleware-infra', '中间件与基础设施适配', 'JWT、Redis、RabbitMQ、限流、数据库、配置和可观测性等横向基础能力。', scan.files.filter((f) => /^internal\/(middleware|db|config|auth|apierror|observability)\//.test(f.path)).map((f) => f.path)],
  ['layer:async-workers', '异步 worker', '消费消息队列、更新缓存和推送通知的后台任务处理器。', scan.files.filter((f) => /^internal\/worker\//.test(f.path)).map((f) => f.path)],
  ['layer:deployment-config', '部署与运行配置', 'Docker、Compose、Go module 和 YAML 配置等构建部署与运行参数。', scan.files.filter((f) => f.fileCategory === 'infra' || f.fileCategory === 'config').map((f) => f.path)],
];

const assigned = new Set();
const layers = layerDefs.map(([id, name, description, paths]) => {
  const nodeIds = ids(paths).filter((id) => !assigned.has(id));
  for (const id of nodeIds) assigned.add(id);
  return { id, name, description, nodeIds };
});
const fileLevel = assembled.nodes.filter((n) => ['file', 'config', 'document', 'service', 'pipeline', 'table', 'schema', 'resource', 'endpoint'].includes(n.type));
const unassigned = fileLevel.map((n) => n.id).filter((id) => !assigned.has(id));
if (unassigned.length) layers.push({ id: 'layer:other', name: '其他文件', description: '未归入主要业务层但仍属于项目结构的文件。', nodeIds: unassigned });

const tour = [
  { order: 1, title: '从 API 入口看启动链路', description: '先看 API 进程如何加载配置、连接 MySQL/Redis/RabbitMQ，并把 Gin 路由挂起来。', nodeIds: ids(['cmd/main.go', 'internal/config/loadConfig.go', 'internal/db/db.go', 'internal/http/router.go']) },
  { order: 2, title: '理解路由到 handler 的分发', description: 'router.go 连接认证、限流和各业务 handler，是理解外部 HTTP API 的最佳入口。', nodeIds: ids(['internal/http/router.go', 'internal/account/handler.go', 'internal/video/video_handler.go', 'internal/feed/handler.go', 'internal/social/handler.go']) },
  { order: 3, title: '追踪视频与 Feed 核心业务', description: '视频、点赞、评论和 Feed 服务共同形成内容流、热度榜和用户互动的核心路径。', nodeIds: ids(['internal/feed/service.go', 'internal/feed/repo.go', 'internal/video/video_service.go', 'internal/video/like_service.go', 'internal/video/comment_service.go']) },
  { order: 4, title: '看账号和社交关系', description: '账号 service、社交 service 与消息模块负责登录身份、关注关系和通知入口。', nodeIds: ids(['internal/account/service.go', 'internal/social/service.go', 'internal/message/handler.go']) },
  { order: 5, title: '理解异步事件处理', description: 'worker 包消费 RabbitMQ 任务，异步维护点赞、评论、关注、Feed timeline、热度和 SSE 通知。', nodeIds: ids(['cmd/worker/main.go', 'internal/worker/likeworker.go', 'internal/worker/commentworker.go', 'internal/worker/socialworker.go', 'internal/worker/notificationworker.go', 'internal/worker/ssehub.go']) },
  { order: 6, title: '检查部署与配置', description: '最后看 Dockerfile、docker-compose 和 YAML 配置，理解本地/容器环境中的依赖服务和运行参数。', nodeIds: ids(['Dockerfile', 'docker-compose.yml', 'configs/config.yaml', 'configs/config.docker.yaml', 'configs/config.compose-local.yaml', 'go.mod']) },
];

const graph = {
  version: '1.0.0',
  project: {
    name: scan.name,
    languages: scan.languages,
    frameworks: scan.frameworks,
    description: scan.description,
    analyzedAt: new Date().toISOString(),
    gitCommitHash: gitCommit(),
  },
  nodes: assembled.nodes,
  edges: assembled.edges,
  layers,
  tour,
};

writeJson(join(intermediate, 'assembled-graph.json'), graph);
writeJson(join(ua, 'knowledge-graph.json'), graph);
writeJson(join(ua, 'meta.json'), {
  lastAnalyzedAt: graph.project.analyzedAt,
  gitCommitHash: graph.project.gitCommitHash,
  version: graph.version,
  analyzedFiles: scan.totalFiles,
});

const withEdges = new Set(graph.edges.flatMap((e) => [e.source, e.target]));
const issues = [];
const warnings = [];
for (const e of graph.edges) {
  if (!nodeIds.has(e.source)) issues.push(`edge source missing: ${e.source}`);
  if (!nodeIds.has(e.target)) issues.push(`edge target missing: ${e.target}`);
}
for (const n of graph.nodes) if (!withEdges.has(n.id)) warnings.push(`orphan node: ${n.id}`);
for (const layer of graph.layers) for (const id of layer.nodeIds) if (!nodeIds.has(id)) issues.push(`layer missing node: ${id}`);
for (const step of graph.tour) for (const id of step.nodeIds) if (!nodeIds.has(id)) issues.push(`tour missing node: ${id}`);
const stats = {
  totalNodes: graph.nodes.length,
  totalEdges: graph.edges.length,
  totalLayers: graph.layers.length,
  tourSteps: graph.tour.length,
  nodeTypes: Object.fromEntries(Object.entries(Object.groupBy(graph.nodes, (n) => n.type)).map(([k, v]) => [k, v.length])),
  edgeTypes: Object.fromEntries(Object.entries(Object.groupBy(graph.edges, (e) => e.type)).map(([k, v]) => [k, v.length])),
};
writeJson(join(intermediate, 'review.json'), { issues, warnings, stats });
console.log(`knowledge-graph nodes=${stats.totalNodes} edges=${stats.totalEdges} layers=${stats.totalLayers} tour=${stats.tourSteps} issues=${issues.length} warnings=${warnings.length}`);



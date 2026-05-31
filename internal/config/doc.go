// Package config 负责加载、合并并校验 research CLI 的运行配置。
//
// 配置合并顺序固定为：代码默认值、可选 YAML 文件、环境变量、命令行显式覆盖。
// 这样可以保证本地开发有可用默认值，部署时又能通过环境变量注入密钥。
//
// cmd/research 只调用 Load；Load 内部会使用 strict YAML decode、环境变量覆盖和 Validate，
// 因此下游模块拿到的 Config 已经满足 provider、输出格式和预算约束。
package config

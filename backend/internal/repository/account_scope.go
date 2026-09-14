package repository

import (
	"strings"

	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	// ordinaryAccountSQLPredicate 是无表别名原生 SQL 的普通账户边界。
	// HF 凭证虽然复用 accounts 表承载公共字段，但只能由专用仓储和两级调度器读取；
	// 普通账户管理、传统调度、后台同步与统计查询必须统一复用本作用域。
	ordinaryAccountSQLPredicate = "platform <> '" + service.PlatformHuggingFace + "'"
	// ordinaryAccountAliasedSQLPredicate 用于 accounts 表别名为 a 的联表查询。
	ordinaryAccountAliasedSQLPredicate = "a.platform <> '" + service.PlatformHuggingFace + "'"
)

// ordinaryAccountPredicate 返回 Ent 查询使用的普通账户边界。
// 将规则集中在仓储层可以形成防御纵深：即使服务层调用方遗漏平台校验，十万级 HF
// 专用凭证也不会进入传统账户列表、调度快照或后台批处理。
func ordinaryAccountPredicate() dbpredicate.Account {
	return dbaccount.PlatformNEQ(service.PlatformHuggingFace)
}

// isOrdinaryAccountPlatform 判断单个平台能否进入普通账户查询。
func isOrdinaryAccountPlatform(platform string) bool {
	return strings.TrimSpace(platform) != service.PlatformHuggingFace
}

// ordinaryAccountPlatforms 清理多平台过滤条件，并剔除 HF 专用平台。
// 顺序保持不变，重复项只保留第一次，避免生成冗余的 IN 参数。
func ordinaryAccountPlatforms(platforms []string) []string {
	out := make([]string, 0, len(platforms))
	seen := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platform = strings.TrimSpace(platform)
		if platform == "" || !isOrdinaryAccountPlatform(platform) {
			continue
		}
		if _, exists := seen[platform]; exists {
			continue
		}
		seen[platform] = struct{}{}
		out = append(out, platform)
	}
	return out
}

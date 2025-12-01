package changelog

import (
	"os"
	"path/filepath"
	"strings"

	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/FloatTech/zbputils/control"
	"github.com/FloatTech/zbputils/ctxext"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

func init() {
	engine := control.AutoRegister(&ctrl.Options[*zero.Ctx]{
		DisableOnDefault: false,
		Brief:            "更新日志",
		Help: "- 查看更新日志{版本号}\n" +
			"查看指定版本的更新日志\n" +
			"Tip: 仅v2.4.1起才提供更新日志",
		PrivateDataFolder: "changelog",
	})

	engine.OnRegex(`^查看更新日志(\S+)$`).SetBlock(true).Limit(ctxext.LimitByUser).Handle(func(ctx *zero.Ctx) {
		version := ctx.State["regex_matched"].([]string)[1]
		version = strings.TrimSpace(version)

		// 构建日志文件路径
		logFilePath := filepath.Join(engine.DataFolder(), version+".txt")

		// 读取日志文件
		content, err := os.ReadFile(logFilePath)
		if err != nil {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("未找到版本", version, "的更新日志文件。"))
			return
		}

		// 发送日志内容
		ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text(string(content)))
	})
}

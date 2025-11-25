// Package wallet 钱包
package wallet

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/FloatTech/AnimeAPI/wallet"
	"github.com/FloatTech/floatbox/binary"
	"github.com/FloatTech/floatbox/file"
	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/FloatTech/zbputils/control"
	"github.com/FloatTech/zbputils/ctxext"
	"github.com/FloatTech/zbputils/img/text"
	"github.com/golang/freetype"
	"github.com/wcharczuk/go-chart/v2"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

func init() {
	en := control.AutoRegister(&ctrl.Options[*zero.Ctx]{
		DisableOnDefault: false,
		Brief:            "钱包",
		Help: "- 查看钱包排名\n" +
			"- 设置货币名称<CurrencyName>\n" +
			"- 查看我的钱包|查看钱包余额[@User]\n" +
			"- 钱包转账<Amount><@User>\n" +
			"单次转账最少10币\n" +
			"转账存在手续费，手续费为转账总金额的2%，向上取整，最低10币\n" +
			"- 管理钱包余额<+|-><Amount>[@User]\n" +
			"仅超级管理员可管理钱包余额\n" +
			"Tip: 0为公款账号",
		PrivateDataFolder: "wallet",
	})
	cachePath := en.DataFolder() + "cache/"
	coinNameFile := en.DataFolder() + "coin_name.txt"
	publicFundsAccount := int64(0)
	go func() {
		_ = os.RemoveAll(cachePath)
		err := os.MkdirAll(cachePath, 0755)
		if err != nil {
			panic(err)
		}
		// 更改硬币名称
		var coinName string
		if file.IsExist(coinNameFile) {
			content, err := os.ReadFile(coinNameFile)
			if err != nil {
				panic(err)
			}
			coinName = binary.BytesToString(content)
		} else {
			// 旧版本数据
			coinName = "ATRI币"
		}
		wallet.SetWalletName(coinName)
	}()

	en.OnFullMatch("查看钱包排名", zero.OnlyGroup).Limit(ctxext.LimitByGroup).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			gid := strconv.FormatInt(ctx.Event.GroupID, 10)
			today := time.Now().Format("20060102")
			drawedFile := cachePath + gid + today + "walletRank.png"
			if file.IsExist(drawedFile) {
				ctx.SendChain(message.Image("file:///" + file.BOTPATH + "/" + drawedFile))
				return
			}
			// 无缓存获取群员列表
			temp := ctx.GetThisGroupMemberListNoCache().Array()
			usergroup := make([]int64, len(temp))
			for i, info := range temp {
				usergroup[i] = info.Get("user_id").Int()
			}
			// 获取钱包信息
			st, err := wallet.GetGroupWalletOf(true, usergroup...)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			if len(st) == 0 {
				ctx.SendChain(message.Text("当前还没有人拥有", wallet.GetWalletName(), "。"))
				return
			} else if len(st) > 10 {
				st = st[:10]
			}
			_, err = file.GetLazyData(text.FontFile, control.Md5File, true)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			b, err := os.ReadFile(text.FontFile)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误: ", err))
				return
			}
			font, err := freetype.ParseFont(b)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			f, err := os.Create(drawedFile)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			var bars []chart.Value
			for _, v := range st {
				if v.Money != 0 {
					bars = append(bars, chart.Value{
						Label: ctx.CardOrNickName(v.UID),
						Value: float64(v.Money),
					})
				}
			}
			err = chart.BarChart{
				Font:  font,
				Title: wallet.GetWalletName() + "排名(1天只刷新1次)",
				Background: chart.Style{
					Padding: chart.Box{
						Top: 40,
					},
				},
				YAxis: chart.YAxis{
					Range: &chart.ContinuousRange{
						Min: 0,
						Max: math.Ceil(bars[0].Value/10) * 10,
					},
				},
				Height:   500,
				BarWidth: 50,
				Bars:     bars,
			}.Render(chart.PNG, f)
			_ = f.Close()
			if err != nil {
				_ = os.Remove(drawedFile)
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			ctx.SendChain(message.Image("file:///" + file.BOTPATH + "/" + drawedFile))
		})
	en.OnPrefix("设置货币名称", zero.OnlyToMe, zero.SuperUserPermission).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			coinName := strings.TrimSpace(ctx.State["args"].(string))
			err := os.WriteFile(coinNameFile, binary.StringToBytes(coinName), 0644)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			wallet.SetWalletName(coinName)
			ctx.SendChain(message.Text("货币名称修改成功。"))
		})

	en.OnPrefix(`管理钱包余额`, zero.SuperUserPermission).SetBlock(true).Limit(ctxext.LimitByGroup).
		Handle(func(ctx *zero.Ctx) {
			param := strings.TrimSpace(ctx.State["args"].(string))

			// 捕获修改的金额
			re := regexp.MustCompile(`^[+-]?\d+$`)
			amount, err := strconv.Atoi(re.FindString(param))
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("输入金额非法。"))
				return
			}

			// 捕获用户QQ号，只支持@事件
			var uidStr string
			if len(ctx.Event.Message) > 1 && ctx.Event.Message[1].Type == "at" {
				uidStr = ctx.Event.Message[1].Data["qq"]
			} else {
				// 没at就修改自己的钱包
				uidStr = strconv.FormatInt(ctx.Event.UserID, 10)
			}

			uidInt, err := strconv.ParseInt(uidStr, 10, 64)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("QQ号处理失败。"))
				return
			}
			if amount+wallet.GetWalletOf(uidInt) < 0 {
				ctx.SendChain(message.Text("对方钱包余额不足，扣款失败。"))
				return
			}
			err = wallet.InsertWalletOf(uidInt, amount)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			// 根据金额正负动态显示增加了或减少了
			action := "调整"
			if amount > 0 {
				action = "增加"
			} else if amount < 0 {
				action = "减少"
			}
			// 获取修改后的余额
			newBalance := wallet.GetWalletOf(uidInt)
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("钱包余额修改成功：用户", uidStr, "的钱包已", action, math.Abs(float64(amount)), wallet.GetWalletName(), "。\n当前用户", uidStr, "余额为", newBalance, wallet.GetWalletName(), "。"))
		})

	en.OnFullMatchGroup([]string{`查看钱包余额`, `查看我的钱包`}).SetBlock(true).Limit(ctxext.LimitByGroup).
		Handle(func(ctx *zero.Ctx) {
			uidInt := ctx.Event.UserID
			money := wallet.GetWalletOf(uidInt)
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("你的钱包有", money, wallet.GetWalletName(), "。"))
		})

	en.OnFullMatch(`查看公款账户余额`, zero.SuperUserPermission).SetBlock(true).Limit(ctxext.LimitByGroup).
		Handle(func(ctx *zero.Ctx) {
			money := wallet.GetWalletOf(publicFundsAccount)
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("公款账户余额为", money, wallet.GetWalletName(), "。"))
		})

	en.OnPrefix(`钱包转账`, zero.OnlyGroup).SetBlock(true).Limit(ctxext.LimitByGroup).
		Handle(func(ctx *zero.Ctx) {
			param := strings.TrimSpace(ctx.State["args"].(string))

			// 捕获修改的金额,amount扣款金额恒正（要注意符号）
			re := regexp.MustCompile(`^[+]?\d+$`)
			amount, err := strconv.Atoi(re.FindString(param))
			if err != nil || amount <= 0 {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("输入金额非法。"))
				return
			}

			// 检查转账金额是否小于10币
			if amount < 10 {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("单次转账金额不能少于10", wallet.GetWalletName(), "。"))
				return
			}

			// 计算手续费：转账金额的2%，向上取整，最低10币
			fee := int(math.Ceil(float64(amount) * 0.02))
			if fee < 10 {
				fee = 10
			}

			// 捕获用户QQ号，只支持@事件
			var uidStr string
			if len(ctx.Event.Message) > 1 && ctx.Event.Message[1].Type == "at" {
				uidStr = ctx.Event.Message[1].Data["qq"]
			} else {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("获取被转方信息失败。"))
				return
			}

			uidInt, err := strconv.ParseInt(uidStr, 10, 64)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("QQ号处理失败。"))
				return
			}

			// 检查是否尝试给自己转账
			if uidInt == ctx.Event.UserID {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("转账目标非法。"))
				return
			}

			// 开始转账流程
			totalDeduction := amount + fee
			if totalDeduction > wallet.GetWalletOf(ctx.Event.UserID) {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("钱包余额不足！\n(本次转账需要额外支付", fee, wallet.GetWalletName(), "手续费，总共需", totalDeduction, wallet.GetWalletName(), ")"))
				return
			}

			err = wallet.InsertWalletOf(ctx.Event.UserID, -totalDeduction)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("扣款时发生意外错误：", err))
				return
			}

			err = wallet.InsertWalletOf(uidInt, amount)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("处理转账时发生意外错误：", err))
				return
			}

			err = wallet.InsertWalletOf(publicFundsAccount, fee)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("处理手续费时发生意外错误：", err))
				return
			}

			// 获取转账后双方的余额
			senderBalance := wallet.GetWalletOf(ctx.Event.UserID)
			receiverBalance := wallet.GetWalletOf(uidInt)

			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("转账成功：成功给"), message.Text(uidStr), message.Text("转账", amount, wallet.GetWalletName(), "，额外扣除了转账手续费", fee, wallet.GetWalletName(), "。\n你的钱包现有", senderBalance, wallet.GetWalletName(), "，对方有", receiverBalance, wallet.GetWalletName(), "。"))
		})
}

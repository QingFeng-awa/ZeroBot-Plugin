package qqwife

import (
	"errors"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"github.com/FloatTech/imgfactory"
	sql "github.com/FloatTech/sqlite"
	control "github.com/FloatTech/zbputils/control"
	"github.com/FloatTech/zbputils/ctxext"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"

	// 画图
	"github.com/FloatTech/floatbox/file"
	"github.com/FloatTech/gg"
	"github.com/FloatTech/zbputils/img/text"

	// 货币系统
	"github.com/FloatTech/AnimeAPI/wallet"
)

// 好感度系统
type favorability struct {
	Userinfo string // 记录用户
	Favor    int    // 好感度
}

func init() {
	// 好感度系统
	engine.OnMessage(zero.NewPattern(nil).Text(`^查好感度`).At().AsRule(), zero.OnlyGroup, getdb).SetBlock(true).Limit(ctxext.LimitByUser).
		Handle(func(ctx *zero.Ctx) {
			patternParsed := ctx.State[zero.KeyPattern].([]zero.PatternParsed)
			fiancee, _ := strconv.ParseInt(patternParsed[1].At(), 10, 64)
			uid := ctx.Event.UserID
			favor, err := 民政局.查好感度(uid, fiancee)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("查询好感度时发生意外错误：", err))
				return
			}
			// 输出结果
			ctx.SendChain(
				message.Text("你与", fiancee, "的好感度为", favor),
			)
		})
	// 礼物系统
	engine.OnRegex(`^买(\S+)?礼物给\s?(\[CQ:at,(?:\S*,)?qq=(\d+)(?:,\S*)?\]|(\d+))`, zero.OnlyGroup, getdb).SetBlock(true).Limit(ctxext.LimitByUser).
		Handle(func(ctx *zero.Ctx) {
			gid := ctx.Event.GroupID
			uid := ctx.Event.UserID
			sex := getUserPronouns(ctx, uid)
			regexMatched := ctx.State["regex_matched"].([]string)
			// 提取目标用户ID
			var targetIDStr string
			if regexMatched[4] != "" {
				targetIDStr = regexMatched[4]
			} else {
				targetIDStr = regexMatched[3]
			}
			gay, _ := strconv.ParseInt(targetIDStr, 10, 64)
			// 获取礼物品质
			giftQuality := "默认"
			if len(regexMatched) > 1 && regexMatched[1] != "" {
				giftQuality = regexMatched[1]
			}
			sendTip := false
			if giftQuality == "默认" {
				giftQuality = "精致"
				sendTip = true
			}

			// 黑名单检查
			if !checkBlacklist(ctx, gay) {
				return
			}
			if gay == uid {
				ctx.Send(message.ReplyWithMessage(ctx.Event.MessageID, message.At(uid), message.Text("你想给自己买什么礼物呢?")))
				return
			}
			// 获取CD
			groupInfo, err := 民政局.查看设置(gid)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("校验CD时时发生意外错误：", err))
				return
			}
			ok, err := 民政局.判断CD(gid, uid, "买礼物", groupInfo.CDtime)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("校验CD时时发生意外错误：", err))
				return
			}
			if !ok {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("你的技能还在CD中..."))
				return
			}
			// 获取好感度
			_, err = 民政局.查好感度(uid, gay)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("查询好感度时发生意外错误：", err))
				return
			}

			// 礼物品质相关参数
			var minCost, maxCost, acceptRate, favorRatio, baseFavor, maxFavorGain, minFavorLoss, maxFavorLoss, giftThreshold, giftTirednessPercent int
			switch giftQuality {
			case "廉价":
				minCost, maxCost = 1, 100            // 花费范围
				acceptRate = 50                      // 成功概率，百分比
				favorRatio = 20                      // 费用转化比例，每花费 favorRatio 币转化为增加 1 好感度
				baseFavor = 1                        // 基础增加好感度
				maxFavorGain = 50                    // 好感度增加上限
				minFavorLoss, maxFavorLoss = 15, 100 // 若失败的好感度扣除范围
				giftThreshold = 200                  // 礼物厌倦触发阈值
				giftTirednessPercent = 40            // 厌倦好感度百分比
			case "普通":
				minCost, maxCost = 100, 1000
				acceptRate = 60
				favorRatio = 15
				baseFavor = 10
				maxFavorGain = 100
				minFavorLoss, maxFavorLoss = 10, 80
				giftThreshold = 400
				giftTirednessPercent = 50
			case "精致":
				minCost, maxCost = 1000, 5000
				acceptRate = 70
				favorRatio = 10
				baseFavor = 15
				maxFavorGain = 500
				minFavorLoss, maxFavorLoss = 8, 60
				giftThreshold = 600
				giftTirednessPercent = 60
			case "奢华":
				minCost, maxCost = 10000, 50000
				acceptRate = 80
				favorRatio = 8
				baseFavor = 50
				maxFavorGain = 1000
				minFavorLoss, maxFavorLoss = 6, 40
				giftThreshold = 800
				giftTirednessPercent = 70
			case "典藏":
				minCost, maxCost = 50000, 500000
				acceptRate = 90
				favorRatio = 6
				baseFavor = 100
				maxFavorGain = 2000
				minFavorLoss, maxFavorLoss = 4, 20
				giftThreshold = 1000
				giftTirednessPercent = 80
			default:
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("商店没有这个品质的礼物哦"))
				return
			}

			// 接入钱包系统
			walletinfo := wallet.GetWalletOf(uid)
			if walletinfo < minCost {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("你钱包没钱啦！需要至少", minCost, wallet.GetWalletName(), "才能购买该品质礼物"))
				return
			}

			// 限制最大消费不超过钱包余额
			if walletinfo < maxCost {
				maxCost = walletinfo
			}

			moneyToFavor := rand.Intn(maxCost-minCost+1) + minCost

			// 判断是否接受礼物
			isAccepted := rand.Intn(100) < acceptRate

			var newFavor int
			if isAccepted {
				// 计算好感度增加
				conversionFavor := moneyToFavor / favorRatio
				calculatedFavor := baseFavor + conversionFavor

				// 礼物厌倦机制
				currentFavor, err := 民政局.查好感度(uid, gay)
				if err == nil && currentFavor >= giftThreshold {
					// 触发礼物厌倦机制，按百分比减少好感度增加
					calculatedFavor = calculatedFavor * giftTirednessPercent / 100
				}

				// 好感度增加上限
				newFavor := calculatedFavor
				if newFavor > maxFavorGain {
					newFavor = maxFavorGain
				}
			} else {
				newFavor = -(rand.Intn(maxFavorLoss-minFavorLoss+1) + minFavorLoss)
			}

			// 记录结果
			err = wallet.InsertWalletOf(uid, -moneyToFavor)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("钱包系统发生意外错误：", err))
				return
			}
			lastfavor, err := 民政局.更新好感度(uid, gay, newFavor)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("好感度数据库发生意外错误：", err))
				return
			}
			// 写入CD
			err = 民政局.记录CD(gid, uid, "买礼物")
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("写入CD时发生意外错误：", err))
			}

			// 输出结果
			if isAccepted {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("你花了", moneyToFavor, wallet.GetWalletName(), "买了一个", giftQuality, "礼物送给了", sex, "，", sex, "接受了礼物，你们的好感度升至", lastfavor, "(+", newFavor, ")"))
			} else {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("你花了", moneyToFavor, wallet.GetWalletName(), "买了一个", giftQuality, "礼物送给了", sex, "，但", sex, "拒绝了礼物，你们的好感度降至", lastfavor, "(", newFavor, ")"))
			}
			if sendTip {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("此外，你可以选定购买的礼物品质，发送 /用法qqwife 了解详细用法。"))
			}
		})
	engine.OnFullMatch("好感度列表", zero.OnlyGroup, getdb).SetBlock(true).Limit(ctxext.LimitByUser).
		Handle(func(ctx *zero.Ctx) {
			uid := ctx.Event.UserID
			fianceeInfo, err := 民政局.getGroupFavorability(uid)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("获取好感度信息时发生意外错误：", err))
				return
			}
			/***********设置图片的大小和底色***********/
			number := len(fianceeInfo)
			if number > 10 {
				number = 10
			}
			fontSize := 50.0
			canvas := gg.NewContext(1150, int(170+(50+70)*float64(number)))
			canvas.SetRGB(1, 1, 1) // 白色
			canvas.Clear()
			/***********下载字体***********/
			data, err := file.GetLazyData(text.BoldFontFile, control.Md5File, true)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("生成好感度列表时发生意外错误：", err))
			}
			/***********设置字体颜色为黑色***********/
			canvas.SetRGB(0, 0, 0)
			/***********设置字体大小,并获取字体高度用来定位***********/
			if err = canvas.ParseFontFace(data, fontSize*2); err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("生成好感度列表时发生意外错误：", err))
				return
			}
			sl, h := canvas.MeasureString("你的好感度排行列表")
			/***********绘制标题***********/
			canvas.DrawString("你的好感度排行列表", (1100-sl)/2, 100) // 放置在中间位置
			canvas.DrawString("————————————————————", 0, 160)
			/***********设置字体大小,并获取字体高度用来定位***********/
			if err = canvas.ParseFontFace(data, fontSize); err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("生成好感度列表时发生意外错误：", err))
				return
			}
			i := 0
			for _, info := range fianceeInfo {
				if i > 9 {
					break
				}
				if info.Userinfo == "" {
					continue
				}
				fianceID, err := strconv.ParseInt(info.Userinfo, 10, 64)
				if err != nil {
					ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("生成好感度列表时发生意外错误：", err))
					return
				}
				if fianceID == 0 {
					continue
				}
				userName := ctx.CardOrNickName(fianceID)
				canvas.SetRGB255(0, 0, 0)
				canvas.DrawString(userName+"("+info.Userinfo+")", 10, float64(180+(50+70)*i))
				canvas.DrawString(strconv.Itoa(info.Favor), 1020, float64(180+60+(50+70)*i))
				// 进度条最大宽度适配10000好感度上限
				maxBarWidth := 1000.0
				barWidth := float64(info.Favor) * maxBarWidth / 10000.0
				if barWidth > maxBarWidth {
					barWidth = maxBarWidth
				}
				canvas.DrawRectangle(10, float64(180+60+(50+70)*i)-h/2, maxBarWidth, 50)
				canvas.SetRGB255(150, 150, 150)
				canvas.Fill()
				canvas.SetRGB255(0, 0, 0)
				canvas.DrawRectangle(10, float64(180+60+(50+70)*i)-h/2, barWidth, 50)
				canvas.SetRGB255(231, 27, 100)
				canvas.Fill()
				i++
			}
			data, err = imgfactory.ToBytes(canvas.Image())
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("生成好感度列表时发生意外错误：", err))
				return
			}
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.ImageBytes(data))
		})

	engine.OnFullMatch("好感度数据整理", zero.SuperUserPermission, getdb).SetBlock(true).Limit(ctxext.LimitByUser).
		Handle(func(ctx *zero.Ctx) {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("开始整理力，请稍等"))
			民政局.Lock()
			defer 民政局.Unlock()
			count, err := 民政局.db.Count("favorability")
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("整理好感度数据时发生意外错误：", err))
				return
			}
			if count == 0 {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("你看起来还没有任何好感度数据呢"))
				return
			}
			favor := favorability{}
			delInfo := make([]string, 0, count*2)
			favorInfo := make(map[string]int, count*2)
			_ = 民政局.db.FindFor("favorability", &favor, "GROUP BY Userinfo", func() error {
				delInfo = append(delInfo, favor.Userinfo)
				// 解析旧数据
				userList := strings.Split(favor.Userinfo, "+")
				maxQQ, _ := strconv.ParseInt(userList[0], 10, 64)
				minQQ, _ := strconv.ParseInt(userList[1], 10, 64)
				if maxQQ > minQQ {
					favor.Userinfo = userList[0] + "+" + userList[1]
				} else {
					favor.Userinfo = userList[1] + "+" + userList[0]
				}
				// 判断是否是重复的
				score, ok := favorInfo[favor.Userinfo]
				if ok {
					if score < favor.Favor {
						favorInfo[favor.Userinfo] = favor.Favor
					}
				} else {
					favorInfo[favor.Userinfo] = favor.Favor
				}
				return nil
			})
			// 删除旧数据
			q, s := sql.QuerySet("WHERE Userinfo", "IN", delInfo)
			err = 民政局.db.Del("favorability", q, s...)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("删除旧好感度数据时发生意外错误：", err))
			}
			for userInfo, favor := range favorInfo {
				favorInfo := favorability{
					Userinfo: userInfo,
					Favor:    favor,
				}
				err = 民政局.db.Insert("favorability", &favorInfo)
				if err != nil {
					userList := strings.Split(userInfo, "+")
					uid1, _ := strconv.ParseInt(userList[0], 10, 64)
					uid2, _ := strconv.ParseInt(userList[1], 10, 64)
					ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("更新用户", ctx.CardOrNickName(uid1), "与用户", ctx.CardOrNickName(uid2), "的好感度时发生意外错误：", err))
				}
			}
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("好感度数据整理完成"))
		})
}

func (sql *婚姻登记) 查好感度(uid, target int64) (int, error) {
	sql.Lock()
	defer sql.Unlock()
	err := sql.db.Create("favorability", &favorability{})
	if err != nil {
		return 0, err
	}
	info := favorability{}
	if uid > target {
		userinfo := strconv.FormatInt(uid, 10) + "+" + strconv.FormatInt(target, 10)
		err = sql.db.Find("favorability", &info, "WHERE Userinfo = ?", userinfo)
		if err != nil {
			_ = sql.db.Find("favorability", &info, "WHERE Userinfo glob ?", "*"+userinfo+"*")
		}
	} else {
		userinfo := strconv.FormatInt(target, 10) + "+" + strconv.FormatInt(uid, 10)
		err = sql.db.Find("favorability", &info, "WHERE Userinfo = ?", userinfo)
		if err != nil {
			_ = sql.db.Find("favorability", &info, "WHERE Userinfo glob ?", "*"+userinfo+"*")
		}
	}
	return info.Favor, nil
}

// 获取好感度数据组
type favorList []favorability

func (s favorList) Len() int {
	return len(s)
}
func (s favorList) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s favorList) Less(i, j int) bool {
	return s[i].Favor > s[j].Favor
}
func (sql *婚姻登记) getGroupFavorability(uid int64) (list favorList, err error) {
	uidStr := strconv.FormatInt(uid, 10)
	sql.RLock()
	defer sql.RUnlock()
	info := favorability{}
	err = sql.db.FindFor("favorability", &info, "WHERE Userinfo glob ?", func() error {
		var target string
		userList := strings.Split(info.Userinfo, "+")
		switch {
		case len(userList) == 0:
			return errors.New("好感度系统数据存在错误")
		case userList[0] == uidStr:
			target = userList[1]
		default:
			target = userList[0]
		}
		list = append(list, favorability{
			Userinfo: target,
			Favor:    info.Favor,
		})
		return nil
	}, "*"+uidStr+"*")
	sort.Sort(list)
	return
}

// 设置好感度 正增负减
func (sql *婚姻登记) 更新好感度(uid, target int64, score int) (favor int, err error) {
	sql.Lock()
	defer sql.Unlock()
	err = sql.db.Create("favorability", &favorability{})
	if err != nil {
		return
	}
	info := favorability{}
	uidstr := strconv.FormatInt(uid, 10)
	targstr := strconv.FormatInt(target, 10)
	if uid > target {
		info.Userinfo = uidstr + "+" + targstr
		err = sql.db.Find("favorability", &info, "WHERE Userinfo = ?", info.Userinfo)
	} else {
		info.Userinfo = targstr + "+" + uidstr
		err = sql.db.Find("favorability", &info, "WHERE Userinfo = ?", info.Userinfo)
	}
	if err != nil {
		err = sql.db.Find("favorability", &info, "WHERE Userinfo glob ?", "*"+targstr+"+"+uidstr+"*")
		if err == nil { // 如果旧数据存在就删除旧数据
			err = 民政局.db.Del("favorability", "WHERE Userinfo = ?", info.Userinfo)
		}
	}
	info.Favor += score
	if info.Favor > 10000 {
		info.Favor = 10000
	} else if info.Favor < 0 {
		info.Favor = 0
	}
	err = sql.db.Insert("favorability", &info)
	return info.Favor, err
}

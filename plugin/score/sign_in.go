// Package score 签到
package score

import (
	"encoding/base64"
	"errors"
	"image"
	"io"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/FloatTech/AnimeAPI/bilibili"
	"github.com/FloatTech/AnimeAPI/wallet"
	"github.com/FloatTech/floatbox/file"
	"github.com/FloatTech/floatbox/process"
	"github.com/FloatTech/floatbox/web"
	"github.com/FloatTech/imgfactory"
	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/FloatTech/zbputils/control"
	"github.com/FloatTech/zbputils/ctxext"
	"github.com/FloatTech/zbputils/img/text"
	"github.com/golang/freetype"
	log "github.com/sirupsen/logrus"
	"github.com/wcharczuk/go-chart/v2"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

const (
	backgroundURL = "https://img.api.qingfengawa.top/"
	referer       = ""
	signinMax     = 1
	// SCOREMAX 分数上限定为1200
	SCOREMAX = 1200
)

var (
	rankArray = [...]int{0, 10, 20, 50, 100, 200, 350, 550, 750, 1000, 1200}
	engine    = control.AutoRegister(&ctrl.Options[*zero.Ctx]{
		DisableOnDefault: false,
		Brief:            "签到",
		Help: "- 签到\n" +
			"签到可获得不定量的币作为奖励，等级越高奖励越多\n" +
			"奖励计算公式为10 + 基于当前等级*100的范围随机取值\n" +
			"每次签到根据等级获取经验值，最少1点，经验达到要求自动进阶下一等级，等级最高为10级\n" +
			"例如1级每次签到就会获得1点经验，2级则为2点经验\n" +
			"- 查看等级排名\n" +
			"等级排名为全局，即跨群排名\n" +
			"- 设置签到预设[0-3]\n" +
			"每个签到预设对应不同的签到图风格，仅超管可切换",
		PrivateDataFolder: "score",
	})
	styles = []scoredrawer{
		drawScore15,
		drawScore16,
		drawScore17,
		drawScore17b2,
	}
)

func init() {
	cachePath := engine.DataFolder() + "cache/"
	go func() {
		sdb = initialize(engine.DataFolder() + "score.db")
		ok := file.IsExist(cachePath)
		if !ok {
			err := os.MkdirAll(cachePath, 0777)
			if err != nil {
				panic(err)
			}
			return
		}
		files, err := os.ReadDir(cachePath)
		if err == nil {
			for _, f := range files {
				if !strings.Contains(f.Name(), time.Now().Format("20060102")) {
					_ = os.Remove(cachePath + f.Name())
				}
			}
		}
	}()
	engine.OnFullMatch(`签到`).Limit(ctxext.LimitByUser).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		k := uint8(0)
		// 使用全局预设，不再依赖群聊ID
		k = uint8(ctx.State["manager"].(*ctrl.Control[*zero.Ctx]).GetData(0))
		if int(k) >= len(styles) {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：签到预设", strconv.Itoa(int(k)), "非法，请设置为合法值。"))
			return
		}
		uid := ctx.Event.UserID
		today := time.Now().Format("20060102")
		// 签到图片
		drawedFile := cachePath + strconv.FormatInt(uid, 10) + today + "signin.png"
		picFile := cachePath + strconv.FormatInt(uid, 10) + today + ".png"
		// 获取签到时间
		si := sdb.GetSignInByUID(uid)
		siUpdateTimeStr := si.UpdatedAt.Format("20060102")
		switch {
		case si.Count >= signinMax && siUpdateTimeStr == today:
			// 如果签到时间是今天
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("今天你已经签到过了！"))
			return
		case siUpdateTimeStr != today:
			// 如果是跨天签到就清数据
			err := sdb.InsertOrUpdateSignInCountByUID(uid, 0)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
				return
			}
		}
		// 更新签到次数
		err := sdb.InsertOrUpdateSignInCountByUID(uid, si.Count+1)
		if err != nil {
			ctx.SendChain(message.Text("发生意外错误：", err))
			return
		}
		// 更新经验
		currentScore := sdb.GetScoreByUID(uid).Score
		// 根据当前等级获取经验，等级越高获得经验越多，最少获得1点经验
		currentRank := getrank(currentScore)
		expToAdd := currentRank
		if expToAdd < 1 {
			expToAdd = 1
		}
		level := currentScore + expToAdd
		if level > SCOREMAX {
			level = SCOREMAX
		}
		err = sdb.InsertOrUpdateScoreByUID(uid, level)
		if err != nil {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
			return
		}
		// 更新钱包
		rank := getrank(level)
		add := 10
		if rank > 0 {
			add += rand.Intn(rank * 100) // 等级越高获得的钱越高
		}
		err = wallet.InsertWalletOf(uid, add)
		if err != nil {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
			return
		}
		alldata := &scdata{
			drawedfile: drawedFile,
			picfile:    picFile,
			uid:        uid,
			nickname:   ctx.CardOrNickName(uid),
			inc:        add,
			score:      wallet.GetWalletOf(uid),
			level:      level,
			rank:       rank,
			expToAdd:   expToAdd,
		}

		// 创建一个channel用于接收图片生成结果
		type drawResult struct {
			image image.Image
			err   error
		}
		resultChan := make(chan drawResult, 1)

		// 启动goroutine进行图片生成
		go func() {
			drawimage, err := styles[k](alldata)
			resultChan <- drawResult{image: drawimage, err: err}
		}()

		// 启动超时提醒goroutine
		timeoutChan := make(chan bool, 1)
		go func() {
			time.Sleep(4 * time.Second)
			timeoutChan <- true
		}()

		var drawimage image.Image
		var drawErr error

		// 等待图片生成完成或超时
		select {
		case result := <-resultChan:
			drawimage, drawErr = result.image, result.err
		case <-timeoutChan:
			// 图片生成超时，提醒用户但不中断生成过程
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("签到成功，但生成签到图可能需要较长时间，请稍等..."))
			// 继续等待图片生成完成
			result := <-resultChan
			drawimage, drawErr = result.image, result.err
		}

		if drawErr != nil {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("签到成功，但签到图生成失败。"))
			return
		}
		// done.
		f, err := os.Create(drawedFile)
		if err != nil {
			data, err := imgfactory.ToBytes(drawimage)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
				return
			}
			ctx.SendChain(message.ImageBytes(data))
			return
		}
		_, err = imgfactory.WriteTo(drawimage, f)
		defer f.Close()
		if err != nil {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
			return
		}
		trySendImage(drawedFile, ctx)
	})
	engine.OnFullMatch("查看等级排名", zero.OnlyGroup).Limit(ctxext.LimitByGroup).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			today := time.Now().Format("20060102")
			drawedFile := cachePath + today + "scoreRank.png"
			if file.IsExist(drawedFile) {
				trySendImage(drawedFile, ctx)
				return
			}
			st, err := sdb.GetScoreRankByTopN(10)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
				return
			}
			if len(st) == 0 {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("目前还没有人签到过哦。"))
				return
			}
			_, err = file.GetLazyData(text.FontFile, control.Md5File, true)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
				return
			}
			b, err := os.ReadFile(text.FontFile)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
				return
			}
			font, err := freetype.ParseFont(b)
			if err != nil {
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
				return
			}
			f, err := os.Create(drawedFile)
			if err != nil {
				ctx.SendChain(message.Text("发生意外错误：", err))
				return
			}
			var bars []chart.Value
			for _, v := range st {
				if v.Score != 0 {
					bars = append(bars, chart.Value{
						Label: ctx.CardOrNickName(v.UID),
						Value: float64(v.Score),
					})
				}
			}
			err = chart.BarChart{
				Font:  font,
				Title: "等级排名(1天只刷新1次)",
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
				ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
				return
			}
			trySendImage(drawedFile, ctx)
		})
	engine.OnRegex(`^设置签到预设\s*(\d+)$`, zero.SuperUserPermission).Limit(ctxext.LimitByUser).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		key := ctx.State["regex_matched"].([]string)[1]
		kn, err := strconv.Atoi(key)
		if err != nil {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
			return
		}
		k := uint8(kn)
		if int(k) >= len(styles) {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("签到预设", key, "不存在。"))
			return
		}
		// 使用全局预设，key=0表示全局设置
		err = ctx.State["manager"].(*ctrl.Control[*zero.Ctx]).SetData(0, int64(k))
		if err != nil {
			ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("发生意外错误：", err))
			return
		}
		ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("已将", key, "设为默认签到预设。"))
	})
}

func getHourWord(t time.Time) string {
	h := t.Hour()
	switch {
	case 6 <= h && h < 12:
		return "早上好"
	case 12 <= h && h < 14:
		return "中午好"
	case 14 <= h && h < 19:
		return "下午好"
	case 19 <= h && h < 24:
		return "晚上好"
	case 0 <= h && h < 6:
		return "凌晨好"
	default:
		return ""
	}
}

func getrank(count int) int {
	for k, v := range rankArray {
		if count == v {
			return k
		} else if count < v {
			return k - 1
		}
	}
	return -1
}

func initPic(picFile string, uid int64) (avatar []byte, err error) {
	defer process.SleepAbout1sTo2s()
	avatar, err = web.GetData("https://q4.qlogo.cn/g?b=qq&nk=" + strconv.FormatInt(uid, 10) + "&s=640")
	if err != nil {
		return
	}
	if file.IsExist(picFile) {
		return
	}
	url, err := bilibili.GetRealURL(backgroundURL)
	if err == nil {
		data, err := web.RequestDataWith(web.NewDefaultClient(), url, "", referer, "", nil)
		if err == nil {
			return avatar, os.WriteFile(picFile, data, 0644)
		}
	}
	// 获取网络图片失败，使用本地已有的图片
	log.Error("[score:get online img error]:", err)
	return avatar, copyImage(picFile)
}

// 使用"file:"发送图片失败后，改用base64发送
func trySendImage(filePath string, ctx *zero.Ctx) {
	filePath = file.BOTPATH + "/" + filePath
	if id := ctx.SendChain(message.Image("file:///" + filePath)); id.ID() != 0 {
		return
	}
	imgFile, err := os.Open(filePath)
	if err != nil {
		ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Reply(ctx.Event.MessageID), message.Text("无法获取签到图片：", err))
		return
	}
	defer imgFile.Close()
	// 使用 base64.NewEncoder 将文件内容编码为 base64 字符串
	var encodedFileData strings.Builder
	encodedFileData.WriteString("base64://")
	encoder := base64.NewEncoder(base64.StdEncoding, &encodedFileData)
	_, err = io.Copy(encoder, imgFile)
	if err != nil {
		ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("编码文件内容失败：", err))
		return
	}
	encoder.Close()
	drawedFileBase64 := encodedFileData.String()
	if id := ctx.SendChain(message.Image(drawedFileBase64)); id.ID() == 0 {
		ctx.SendChain(message.Reply(ctx.Event.MessageID), message.Text("无法读取图片文件：", err))
		return
	}
}

// 从已有签到背景中，复制出一张图片
func copyImage(picFile string) (err error) {
	// 读取目录中的文件列表,并随机挑选出一张图片
	cachePath := engine.DataFolder() + "cache/"
	files, err := os.ReadDir(cachePath)
	if err != nil {
		return err
	}

	// 随机取10次图片，取到图片就break退出
	imgNum := len(files)
	if imgNum == 0 {
		return errors.New("copyImage: no local image")
	}
	var validFile string
	for i := 0; i < len(files) && i < 10; i++ {
		imgFile := files[rand.Intn(imgNum)]
		if !imgFile.IsDir() && strings.HasSuffix(imgFile.Name(), ".png") && !strings.HasSuffix(imgFile.Name(), "signin.png") {
			validFile = imgFile.Name()
			break
		}
	}
	if len(validFile) == 0 {
		return errors.New("copyImage: no local image")
	}
	selectedFile := cachePath + validFile

	// 使用 io.Copy 复制签到背景
	srcFile, err := os.Open(selectedFile)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(picFile)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	return err
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"gopkg.in/yaml.v3"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// ConfYaml 用于解析 YAML 配置文件中的配置部分
type ConfYaml struct {
	Config Config `yaml:"config"`
}

// Config 存储配置信息，如 URL、超时时间、轮询间隔、Chrome路径、推送配置
type Config struct {
	Url      []string `yaml:"url"`
	Timeout  int      `yaml:"timeout"`
	Polling  int      `yaml:"polling"`
	Chrome   string   `yaml:"chrome"`
	Pushplus Pushplus `yaml:"pushplus"`
}

// Pushplus 存储推送配置，包含Token、标题、群组
type Pushplus struct {
	Token string `yaml:"token"`
	Title string `yaml:"title"`
	Topic int    `yaml:"topic"`
}

// pushRequest 是用于发送推送通知的结构体，包含 Token、标题、内容 、模版、群组
type pushRequest struct {
	Token    string `json:"token"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Template string `json:"template"`
	Topic    int    `json:"topic"`
}

// pushResponse 存储推送服务的响应结果，如状态码、消息
type pushResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// 全局变量 config 用于存储配置信息
var config Config

// parseYaml 用于解析 YAML 配置文件并返回配置部分
func parseYaml(file string) Config {
	config := new(ConfYaml)
	b, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}
	err = yaml.Unmarshal(b, &config)
	if err != nil {
		log.Fatal(err)
	}
	return config.Config
}

// 初始化函数，用于读取配置文件
func init() {
	config = parseYaml("conf.yaml")
}

// 主函数，程序的入口点
func main() {
	// 设置日志文件和输出
	logFile, err := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)
	// 启动定时任务
	tickerTask()
}

// pushPlusNotify 用于发送推送通知到 Pushplus 服务
func pushPlusNotify(msg string) error {
	httpClient := &http.Client{}
	url := "http://www.pushplus.plus/send"
	title := config.Pushplus.Title
	token := config.Pushplus.Token
	topic := config.Pushplus.Topic
	data := pushRequest{Token: token, Title: title, Content: msg, Template: "html", Topic: topic}
	reqBody, err := json.Marshal(data)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	bodyText, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	log.Println("pushplus: " + string(bodyText))
	var push pushResponse
	err = json.Unmarshal(bodyText, &push)
	if err != nil {
		return err
	}
	if push.Code != 200 {
		return errors.New(push.Msg)
	}
	return nil
}

// tickerTask 启动定时任务，定期检查网站状态
func tickerTask() {
	ticker := time.NewTicker(time.Duration(config.Polling) * time.Second)
	for {
		select {
		case <-ticker.C:
			// 遍历配置的 URL，检查每个网站的状态
			for _, url := range config.Url {
				duration, err := pageMonitor(url)
				log.Println("pageMonitor: " + url + " " + strconv.FormatFloat(duration.Seconds(), 'f', 2, 64) + "s")
				// 根据检查结果发送推送通知
				if err != nil {
					err := pushPlusNotify("<b>通知:</b> " + url + " <strong>网站无法访问!</strong>" + "</br>" + "<b>事件时间:</b> " + time.Now().Format("2006-01-02 15:04:05") + "</br>" + "<b>错误代码:</b> " + err.Error())
					if err != nil {
						log.Println(err)
					}
				}
				if duration.Seconds() > float64(config.Timeout) {
					err := pushPlusNotify("<b>通知:</b> " + url + " <strong>网站超时访问!</strong>" + "</br>" + "<b>事件时间:</b> " + time.Now().Format("2006-01-02 15:04:05") + "</br>" + "<b>错误代码:</b> " + "访问时间" + strconv.FormatFloat(duration.Seconds(), 'f', 2, 64) + "s")
					if err != nil {
						log.Println(err)
					}
				}
			}
		}
	}

}

// pageMonitor 用于监测一个页面的加载时间，返回加载时间和错误信息
func pageMonitor(url string) (time.Duration, error) {
	start := time.Now()
	// 配置并启动一个无头 Chrome 实例
	u := launcher.New().
		Leakless(true).
		Set("disable-gpu", "true").
		Set("ignore-certificate-errors", "true").
		Set("ignore-certificate-errors", "1").
		Set("disable-crash-reporter", "true").
		Set("disable-notifications", "true").
		Set("hide-scrollbars", "true").
		Set("window-size", fmt.Sprintf("%d,%d", 1080, 1920)).
		Set("mute-audio", "true").
		Set("incognito", "true").
		Bin(config.Chrome).
		NoSandbox(true).
		Headless(true).
		MustLaunch()
	browser := rod.New().ControlURL(u).MustConnect()
	defer browser.MustClose()
	page := browser.MustPage()
	err := rod.Try(func() {
		page.Timeout(20 * time.Second).MustNavigate(url).MustWaitLoad()
	})
	defer page.MustClose()
	// 根据监测结果返回加载时间和可能的错误
	if errors.Is(err, context.DeadlineExceeded) {
		return 0, errors.New("timeout")

	}
	if errors.Is(err, &rod.NavigationError{Reason: "net::ERR_NAME_NOT_RESOLVED"}) {
		return 0, errors.New("offline")
	}
	if err != nil {
		return 0, err
	}
	duration := time.Since(start)
	return duration, nil
}

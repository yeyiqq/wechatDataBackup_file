package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/protobuf/proto"
	"wechatDataBackup/pkg/wechat"
)


// 消息类型常量
const (
	Wechat_Message_Type_Text       = 1
	Wechat_Message_Type_Picture    = 3
	Wechat_Message_Type_Voice      = 34
	Wechat_Message_Type_Visit_Card = 42
	Wechat_Message_Type_Video      = 43
	Wechat_Message_Type_Emoji      = 47
	Wechat_Message_Type_Location   = 48
	Wechat_Message_Type_Misc       = 49
	Wechat_Message_Type_Voip       = 50
	Wechat_Message_Type_System     = 10000
)

const (
	Wechat_Misc_Message_File           = 6
	Wechat_Misc_Message_CustomEmoji    = 8
	Wechat_Misc_Message_ShareEmoji     = 15
	Wechat_Misc_Message_ForwardMessage = 19
	Wechat_Misc_Message_Applet         = 33
	Wechat_Misc_Message_Applet2        = 36
	Wechat_Misc_Message_Channels       = 51
	Wechat_Misc_Message_Refer          = 57
	Wechat_Misc_Message_Live           = 63
	Wechat_Misc_Message_Game           = 68
	Wechat_Misc_Message_Notice         = 87
	Wechat_Misc_Message_Live2          = 88
	Wechat_Misc_Message_TingListen     = 92
	Wechat_Misc_Message_Transfer       = 2000
	Wechat_Misc_Message_RedPacket      = 2003
)

// 数据结构定义
type Dialogue struct {
	Index   int    `json:"index"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	Time    string `json:"time"`
}

type ChatSession struct {
	Instruction string     `json:"instruction"`
	Dialogue    []Dialogue `json:"dialogue"`
}

type WeChatMessage struct {
	LocalId    int    `json:"LocalId"`
	MsgSvrId   string `json:"MsgSvrId"`
	Type       int    `json:"type"`
	SubType    int    `json:"SubType"`
	IsSender   int    `json:"IsSender"`
	CreateTime int64  `json:"createTime"`
	Talker     string `json:"talker"`
	Content    string `json:"content"`
	ThumbPath  string `json:"ThumbPath"`
	ImagePath  string `json:"ImagePath"`
	VideoPath  string `json:"VideoPath"`
	VoicePath  string `json:"VoicePath"`
	EmojiPath  string `json:"EmojiPath"`
	FileInfo   struct {
		FilePath string `json:"FilePath"`
		FileName string `json:"FileName"`
	} `json:"fileInfo"`
	IsChatRoom bool `json:"isChatRoom"`
	UserInfo   struct {
		UserName string `json:"UserName"`
		NickName string `json:"NickName"`
	} `json:"userInfo"`
	// 添加用于存储解析后的文件路径
	bytesExtra []byte
}

type Contact struct {
	UserName string
	NickName string
	MessageCount int
}

type ChatExtractor struct {
	DataPath        string
	SelfWxId        string
	TargetWxId      string
	MessageDBs      []*sql.DB
	MicroMsgDB      *sql.DB
	UserDataDB      *sql.DB
	FileStoragePath string
}

// 创建聊天记录提取器
func NewChatExtractor(dataPath, selfWxId, targetWxId string) (*ChatExtractor, error) {
	extractor := &ChatExtractor{
		DataPath:        dataPath,
		SelfWxId:        selfWxId,
		TargetWxId:      targetWxId,
		FileStoragePath: filepath.Join(dataPath, "FileStorage"),
	}

	// 打开MicroMsg.db
	microMsgPath := filepath.Join(dataPath, "Msg", "MicroMsg.db")
	microMsgDB, err := sql.Open("sqlite3", microMsgPath)
	if err != nil {
		return nil, fmt.Errorf("打开MicroMsg.db失败: %v", err)
	}
	extractor.MicroMsgDB = microMsgDB

	// 打开UserData.db
	userDataPath := filepath.Join(dataPath, "Msg", "UserData.db")
	userDataDB, err := sql.Open("sqlite3", userDataPath)
	if err != nil {
		return nil, fmt.Errorf("打开UserData.db失败: %v", err)
	}
	extractor.UserDataDB = userDataDB

	// 打开MSG数据库
	msgDir := filepath.Join(dataPath, "Msg", "Multi")
	extractor.MessageDBs = make([]*sql.DB, 0)

	// 查找所有MSG*.db文件
	for i := 0; ; i++ {
		var msgPath string
		if i == 0 {
			// 先尝试MSG.db，再尝试MSG0.db
			msgPath = filepath.Join(msgDir, "MSG.db")
			if _, err := os.Stat(msgPath); os.IsNotExist(err) {
				msgPath = filepath.Join(msgDir, "MSG0.db")
			}
		} else {
			msgPath = filepath.Join(msgDir, fmt.Sprintf("MSG%d.db", i))
		}

		if _, err := os.Stat(msgPath); os.IsNotExist(err) {
			break
		}

		db, err := sql.Open("sqlite3", msgPath)
		if err != nil {
			log.Printf("打开%s失败: %v", msgPath, err)
			continue
		}

		// 检查数据库中是否有目标用户的消息
		var count int
		query := "SELECT COUNT(*) FROM MSG WHERE StrTalker = ?"
		err = db.QueryRow(query, targetWxId).Scan(&count)
		if err != nil {
			log.Printf("查询%s中的消息失败: %v", msgPath, err)
			db.Close()
			continue
		}

		if count > 0 {
			extractor.MessageDBs = append(extractor.MessageDBs, db)
			log.Printf("找到数据库 %s，包含 %d 条消息", msgPath, count)
		} else {
			db.Close()
		}
	}

	if len(extractor.MessageDBs) == 0 {
		return nil, fmt.Errorf("未找到包含目标用户 %s 的消息数据库", targetWxId)
	}

	return extractor, nil
}

// 关闭数据库连接
func (ce *ChatExtractor) Close() {
	if ce.MicroMsgDB != nil {
		ce.MicroMsgDB.Close()
	}
	if ce.UserDataDB != nil {
		ce.UserDataDB.Close()
	}
	for _, db := range ce.MessageDBs {
		if db != nil {
			db.Close()
		}
	}
}

// 获取用户信息
func (ce *ChatExtractor) GetUserInfo(wxId string) (string, error) {
	query := "SELECT NickName FROM Contact WHERE UserName = ?"
	var nickName string
	err := ce.MicroMsgDB.QueryRow(query, wxId).Scan(&nickName)
	if err != nil {
		log.Printf("查询用户 %s 的昵称失败: %v", wxId, err)
		return wxId, err // 如果查询失败，返回原始wxId
	}
	// 如果昵称为空，使用wxid
	if nickName == "" {
		log.Printf("用户 %s 的昵称为空，使用微信ID", wxId)
		return wxId, nil
	}
	log.Printf("找到用户 %s 的昵称: %s", wxId, nickName)
	return nickName, nil
}

// 格式化时间戳
func formatTime(timestamp int64) string {
	t := time.Unix(timestamp, 0)
	return t.Format("2006-01-02 15:04:05")
}

// 构建完整文件路径
func (ce *ChatExtractor) buildFullPath(filePath string) string {
	if filePath == "" {
		return ""
	}
	
	// 如果路径是绝对路径，直接返回
	if filepath.IsAbs(filePath) {
		return filePath
	}
	
	// 如果是相对路径，构建完整路径
	return filepath.Join(ce.DataPath, filePath)
}

// 查找图片文件
func (ce *ChatExtractor) findImageFile(msgSvrId string) string {
	var foundPath string
	
	// 在Cache目录中查找图片
	cacheDir := filepath.Join(ce.FileStoragePath, "Cache")
	filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			foundPath = path
			log.Printf("找到图片文件: %s", path)
			return filepath.SkipDir
		}
		return nil
	})
	
	if foundPath != "" {
		return foundPath
	}
	
	// 在MsgAttach目录中查找
	msgAttachDir := filepath.Join(ce.FileStoragePath, "MsgAttach")
	filepath.Walk(msgAttachDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			foundPath = path
			log.Printf("在MsgAttach中找到图片文件: %s", path)
			return filepath.SkipDir
		}
		return nil
	})
	
	if foundPath == "" {
		log.Printf("未找到MsgSvrId为 %s 的图片文件", msgSvrId)
	}
	
	return foundPath
}

// 查找视频文件
func (ce *ChatExtractor) findVideoFile(msgSvrId string) string {
	var foundPath string
	
	// 在MsgAttach目录中查找视频文件
	msgAttachDir := filepath.Join(ce.FileStoragePath, "MsgAttach")
	filepath.Walk(msgAttachDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			foundPath = path
			return filepath.SkipDir
		}
		return nil
	})
	
	return foundPath
}

// 查找文件
func (ce *ChatExtractor) findFile(msgSvrId, fileName string) string {
	var foundPath string
	
	// 在File目录中查找
	fileDir := filepath.Join(ce.FileStoragePath, "File")
	filepath.Walk(fileDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && (strings.Contains(path, msgSvrId) || strings.Contains(path, fileName)) {
			foundPath = path
			return filepath.SkipDir
		}
		return nil
	})
	
	return foundPath
}

// 查找转发消息相关文件
func (ce *ChatExtractor) findForwardMessageFile(msgSvrId string) string {
	var foundPath string
	
	// 在MsgAttach目录中查找
	msgAttachDir := filepath.Join(ce.FileStoragePath, "MsgAttach")
	filepath.Walk(msgAttachDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			foundPath = path
			return filepath.SkipDir
		}
		return nil
	})
	
	return foundPath
}

// 查找视频号相关文件
func (ce *ChatExtractor) findChannelsFile(msgSvrId string) string {
	var foundPath string
	
	// 在MsgAttach目录中查找
	msgAttachDir := filepath.Join(ce.FileStoragePath, "MsgAttach")
	filepath.Walk(msgAttachDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			foundPath = path
			return filepath.SkipDir
		}
		return nil
	})
	
	return foundPath
}

// 查找表情文件
func (ce *ChatExtractor) findEmojiFile(msgSvrId string) string {
	var foundPath string
	
	// 在MsgAttach目录中查找表情文件
	msgAttachDir := filepath.Join(ce.FileStoragePath, "MsgAttach")
	filepath.Walk(msgAttachDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			foundPath = path
			return filepath.SkipDir
		}
		return nil
	})
	
	return foundPath
}

// 转换.dat文件为可观看的图片格式
func (ce *ChatExtractor) convertDatToImage(originalPath, msgSvrId string) string {
	// 如果文件不存在，返回原始路径
	if _, err := os.Stat(originalPath); os.IsNotExist(err) {
		return originalPath
	}
	
	// 如果文件不是.dat格式，直接返回原始路径
	if !strings.HasSuffix(strings.ToLower(originalPath), ".dat") {
		return originalPath
	}
	
	// 创建目标目录
	targetDir := filepath.Join(ce.FileStoragePath, "Image")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.Printf("创建图片目录失败: %v", err)
		return originalPath
	}
	
	// 生成目标文件名（使用MsgSvrId作为文件名）
	fileName := filepath.Base(originalPath)
	nameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	targetFileName := nameWithoutExt
	
	// 创建临时文件用于解密
	tempFile := filepath.Join(targetDir, "temp_"+targetFileName)
	defer os.Remove(tempFile) // 清理临时文件
	
	// 解密.dat文件到临时文件
	err := wechat.DecryptDat(originalPath, tempFile)
	if err != nil {
		log.Printf("解密图片文件失败 %s: %v", originalPath, err)
		return originalPath
	}
	
	// 读取解密后的数据
	decryptedData, err := os.ReadFile(tempFile)
	if err != nil {
		log.Printf("读取解密后的图片文件失败 %s: %v", tempFile, err)
		return originalPath
	}
	
	// 检测图片格式并保存
	var targetPath string
	var extension string
	
	// 检测文件头确定图片格式
	if len(decryptedData) >= 4 {
		if decryptedData[0] == 0xFF && decryptedData[1] == 0xD8 {
			extension = ".jpeg"
		} else if decryptedData[0] == 0x89 && decryptedData[1] == 0x50 && decryptedData[2] == 0x4E && decryptedData[3] == 0x47 {
			extension = ".png"
		} else if decryptedData[0] == 0x47 && decryptedData[1] == 0x49 && decryptedData[2] == 0x46 {
			extension = ".gif"
		} else {
			// 默认使用.jpeg格式
			extension = ".jpeg"
		}
	} else {
		extension = ".jpeg"
	}
	
	targetPath = filepath.Join(targetDir, targetFileName+extension)
	
	// 如果目标文件已存在，直接返回
	if _, err := os.Stat(targetPath); err == nil {
		log.Printf("图片已存在，使用缓存: %s", targetPath)
		return targetPath
	}
	
	// 保存解密后的图片
	err = os.WriteFile(targetPath, decryptedData, 0644)
	if err != nil {
		log.Printf("保存图片失败 %s: %v", targetPath, err)
		return originalPath
	}
	
	log.Printf("成功转换图片: %s -> %s", originalPath, targetPath)
	return targetPath
}

// 智能查找真实存在的文件路径
func (ce *ChatExtractor) findRealFilePath(originalPath, msgSvrId string) string {
	var foundPath string
	
	// 首先尝试原始路径
	fullPath := filepath.Join(ce.DataPath, originalPath)
	if _, err := os.Stat(fullPath); err == nil {
		log.Printf("找到文件: %s", fullPath)
		return fullPath
	}
	
	// 如果原始路径不存在，尝试不同的目录变体
	basePath := filepath.Dir(originalPath)
	fileName := filepath.Base(originalPath)
	
	// 可能的目录变体
	possibleDirs := []string{
		"Thumb", "Image", "Video", "File", "Voice", "Cache",
	}
	
	for _, dir := range possibleDirs {
		// 替换路径中的目录名
		newPath := strings.Replace(originalPath, filepath.Base(basePath), dir, 1)
		testPath := filepath.Join(ce.DataPath, newPath)
		
		if _, err := os.Stat(testPath); err == nil {
			log.Printf("找到文件 (目录变体 %s): %s", dir, testPath)
			return testPath
		}
	}
	
	// 如果还是找不到，尝试在MsgAttach目录中搜索包含MsgSvrId的文件
	msgAttachDir := filepath.Join(ce.FileStoragePath, "MsgAttach")
	filepath.Walk(msgAttachDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			// 检查文件名是否匹配
			if strings.Contains(strings.ToLower(info.Name()), strings.ToLower(fileName)) {
				foundPath = path
				log.Printf("找到文件 (MsgSvrId匹配): %s", path)
				return filepath.SkipDir
			}
		}
		return nil
	})
	
	if foundPath != "" {
		return foundPath
	}
	
	// 最后尝试在Cache目录中搜索
	cacheDir := filepath.Join(ce.FileStoragePath, "Cache")
	filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, msgSvrId) {
			foundPath = path
			log.Printf("找到文件 (Cache目录): %s", path)
			return filepath.SkipDir
		}
		return nil
	})
	
	if foundPath == "" {
		log.Printf("未找到文件: %s (MsgSvrId: %s)", originalPath, msgSvrId)
	}
	
	return foundPath
}

// 解析BytesExtra字段获取文件路径和发送者信息
func (ce *ChatExtractor) parseBytesExtra(msg *WeChatMessage) {
	if len(msg.bytesExtra) == 0 {
		return
	}

	var extra wechat.MessageBytesExtra
	err := proto.Unmarshal(msg.bytesExtra, &extra)
	if err != nil {
		log.Printf("解析BytesExtra失败: %v", err)
		return
	}

	for _, ext := range extra.Message2 {
		switch ext.Field1 {
		case 1: // 群聊消息发送者信息
			if msg.IsChatRoom {
				msg.UserInfo.UserName = ext.Field2
			}
		case 3: // ThumbPath
			if len(ext.Field2) > 0 {
				if msg.Type == Wechat_Message_Type_Picture || msg.Type == Wechat_Message_Type_Video || msg.Type == Wechat_Message_Type_Misc {
					// 移除用户名前缀，构建完整路径
					path := ext.Field2
					if strings.HasPrefix(path, ce.SelfWxId) {
						path = path[len(ce.SelfWxId):]
					}
					
					// 智能查找真实存在的文件
					realPath := ce.findRealFilePath(path, msg.MsgSvrId)
					if realPath != "" {
						msg.ThumbPath = realPath
					} else {
						msg.ThumbPath = filepath.Join(ce.DataPath, path)
					}
				}
			}
		case 4: // ImagePath/VideoPath/FilePath
			if len(ext.Field2) > 0 {
				// 移除用户名前缀，构建完整路径
				path := ext.Field2
				if strings.HasPrefix(path, ce.SelfWxId) {
					path = path[len(ce.SelfWxId):]
				}
				
				// 智能查找真实存在的文件
				realPath := ce.findRealFilePath(path, msg.MsgSvrId)
				if realPath == "" {
					realPath = filepath.Join(ce.DataPath, path)
				}
				
				if msg.Type == Wechat_Message_Type_Misc && msg.SubType == Wechat_Misc_Message_File {
					msg.FileInfo.FilePath = realPath
					msg.FileInfo.FileName = filepath.Base(ext.Field2)
				} else if msg.Type == Wechat_Message_Type_Picture || msg.Type == Wechat_Message_Type_Video || msg.Type == Wechat_Message_Type_Misc {
					msg.ImagePath = realPath
					msg.VideoPath = realPath
				}
			}
		}
	}

	// 语音文件路径
	if msg.Type == Wechat_Message_Type_Voice {
		msg.VoicePath = filepath.Join(ce.FileStoragePath, "Voice", msg.MsgSvrId+".mp3")
	}
}

// 解析群聊消息中的发送者
func (ce *ChatExtractor) parseGroupMessageSender(msg WeChatMessage, isSelf bool, selfNickName, targetNickName string) string {
	// 如果是自己发送的消息
	if isSelf {
		return selfNickName
	}
	
	// 优先使用从BytesExtra中解析出的发送者信息
	if msg.UserInfo.UserName != "" {
		// 获取发送者的昵称
		senderNickName, err := ce.GetUserInfo(msg.UserInfo.UserName)
		if err != nil {
			log.Printf("获取发送者 %s 的昵称失败: %v", msg.UserInfo.UserName, err)
			// 如果获取昵称失败，使用微信ID
			return msg.UserInfo.UserName
		}
		return senderNickName
	}
	
	// 如果BytesExtra中没有发送者信息，尝试从消息内容中解析
	text := msg.Content
	
	// 尝试从消息内容中解析发送者
	// 群聊消息格式通常是：发送者昵称: 消息内容
	// 或者：@发送者昵称 消息内容
	
	// 查找冒号分隔符
	colonIndex := strings.Index(text, ":")
	if colonIndex > 0 && colonIndex < 50 { // 限制在合理范围内
		// 检查冒号前的内容是否像昵称
		potentialSender := strings.TrimSpace(text[:colonIndex])
		// 过滤掉一些明显不是昵称的内容
		if !strings.Contains(potentialSender, "[") && 
		   !strings.Contains(potentialSender, "]") &&
		   !strings.Contains(potentialSender, "系统消息") &&
		   !strings.Contains(potentialSender, "撤回了一条消息") &&
		   len(potentialSender) > 0 && len(potentialSender) < 30 {
			return potentialSender
		}
	}
	
	// 查找@符号
	atIndex := strings.Index(text, "@")
	if atIndex >= 0 {
		// 找到@符号后的内容
		afterAt := text[atIndex+1:]
		// 查找空格或换行符作为分隔符
		spaceIndex := strings.IndexAny(afterAt, " \n")
		if spaceIndex > 0 {
			potentialSender := strings.TrimSpace(afterAt[:spaceIndex])
			// 过滤掉一些明显不是昵称的内容
			if !strings.Contains(potentialSender, "[") && 
			   !strings.Contains(potentialSender, "]") &&
			   !strings.Contains(potentialSender, "系统消息") &&
			   !strings.Contains(potentialSender, "撤回了一条消息") &&
			   len(potentialSender) > 0 && len(potentialSender) < 50 {
				return potentialSender
			}
		}
	}
	
	// 调试信息：打印无法解析的消息
	fmt.Printf("DEBUG: 无法解析群聊消息发送者: text='%s', isSelf=%v, UserInfo.UserName='%s'\n", text, isSelf, msg.UserInfo.UserName)
	
	// 如果无法解析，返回目标用户昵称
	return targetNickName
}

// 获取消息内容文本（简化版，不解析BytesExtra）
func (ce *ChatExtractor) GetMessageText(msg WeChatMessage) string {
	switch msg.Type {
	case Wechat_Message_Type_Text:
		return msg.Content
	case Wechat_Message_Type_Picture:
		// 使用解析后的图片路径
		if msg.ImagePath != "" {
			convertedPath := ce.convertDatToImage(msg.ImagePath, msg.MsgSvrId)
			return fmt.Sprintf("[图片] %s", convertedPath)
		}
		if msg.ThumbPath != "" {
			convertedPath := ce.convertDatToImage(msg.ThumbPath, msg.MsgSvrId)
			return fmt.Sprintf("[图片] %s", convertedPath)
		}
		// 尝试查找图片文件
		imagePath := ce.findImageFile(msg.MsgSvrId)
		if imagePath != "" {
			convertedPath := ce.convertDatToImage(imagePath, msg.MsgSvrId)
			return fmt.Sprintf("[图片] %s", convertedPath)
		}
		return "[图片]"
	case Wechat_Message_Type_Voice:
		// 使用解析后的语音路径
		if msg.VoicePath != "" {
			return fmt.Sprintf("[语音] %s", msg.VoicePath)
		}
		// 尝试默认语音路径
		defaultVoicePath := filepath.Join(ce.FileStoragePath, "Voice", msg.MsgSvrId+".mp3")
		if _, err := os.Stat(defaultVoicePath); err == nil {
			return fmt.Sprintf("[语音] %s", defaultVoicePath)
		}
		return "[语音]"
	case Wechat_Message_Type_Video:
		// 使用解析后的视频路径
		if msg.VideoPath != "" {
			return fmt.Sprintf("[视频] %s", msg.VideoPath)
		}
		if msg.ThumbPath != "" {
			return fmt.Sprintf("[视频] %s", msg.ThumbPath)
		}
		// 尝试查找视频文件
		videoPath := ce.findVideoFile(msg.MsgSvrId)
		if videoPath != "" {
			return fmt.Sprintf("[视频] %s", videoPath)
		}
		return "[视频]"
	case Wechat_Message_Type_Emoji:
		// 尝试查找表情文件
		emojiPath := ce.findEmojiFile(msg.MsgSvrId)
		if emojiPath != "" {
			return fmt.Sprintf("[表情] %s", emojiPath)
		}
		return "[表情]"
	case Wechat_Message_Type_Location:
		return "[位置] " + msg.Content
	case Wechat_Message_Type_Misc:
		switch msg.SubType {
		case Wechat_Misc_Message_File:
			// 使用解析后的文件路径
			if msg.FileInfo.FilePath != "" {
				return fmt.Sprintf("[文件] %s %s", msg.FileInfo.FileName, msg.FileInfo.FilePath)
			}
			// 尝试查找文件
			filePath := ce.findFile(msg.MsgSvrId, msg.FileInfo.FileName)
			if filePath != "" {
				return fmt.Sprintf("[文件] %s %s", msg.FileInfo.FileName, filePath)
			}
			return "[文件] " + msg.FileInfo.FileName
		case Wechat_Misc_Message_CustomEmoji, Wechat_Misc_Message_ShareEmoji:
			return "[自定义表情]"
		case Wechat_Misc_Message_ForwardMessage:
			// 使用解析后的路径
			if msg.ThumbPath != "" {
				return fmt.Sprintf("[转发消息] %s %s", msg.Content, msg.ThumbPath)
			}
			// 尝试查找转发消息相关文件
			forwardPath := ce.findForwardMessageFile(msg.MsgSvrId)
			if forwardPath != "" {
				return fmt.Sprintf("[转发消息] %s %s", msg.Content, forwardPath)
			}
			return "[转发消息] " + msg.Content
		case Wechat_Misc_Message_Applet, Wechat_Misc_Message_Applet2:
			return "[小程序] " + msg.Content
		case Wechat_Misc_Message_Channels, Wechat_Misc_Message_Live, Wechat_Misc_Message_Live2:
			// 使用解析后的路径
			if msg.ThumbPath != "" {
				return fmt.Sprintf("[视频号] %s %s", msg.Content, msg.ThumbPath)
			}
			// 尝试查找视频号相关文件
			channelsPath := ce.findChannelsFile(msg.MsgSvrId)
			if channelsPath != "" {
				return fmt.Sprintf("[视频号] %s %s", msg.Content, channelsPath)
			}
			return "[视频号] " + msg.Content
		case Wechat_Misc_Message_Game:
			return "[游戏] " + msg.Content
		case Wechat_Misc_Message_Transfer:
			return "[转账] " + msg.Content
		case Wechat_Misc_Message_RedPacket:
			return "[红包] " + msg.Content
		default:
			return msg.Content
		}
	case Wechat_Message_Type_Voip:
		return "[通话]"
	case Wechat_Message_Type_System:
		return "[系统消息] " + msg.Content
	default:
		return msg.Content
	}
}

// 从所有数据库中获取消息
func (ce *ChatExtractor) GetAllMessages() ([]WeChatMessage, error) {
	var allMessages []WeChatMessage

	for _, db := range ce.MessageDBs {
		query := `
			SELECT localId, MsgSvrID, Type, SubType, IsSender, CreateTime, 
				   ifnull(StrTalker,'') as StrTalker, 
				   ifnull(StrContent,'') as StrContent,
				   ifnull(CompressContent,'') as CompressContent,
				   ifnull(BytesExtra,'') as BytesExtra
			FROM MSG 
			WHERE StrTalker = ?
			ORDER BY CreateTime ASC
		`

		rows, err := db.Query(query, ce.TargetWxId)
		if err != nil {
			log.Printf("查询消息失败: %v", err)
			continue
		}

		for rows.Next() {
			var msg WeChatMessage
			var localId, msgSvrID, msgType, subType, isSender int
			var createTime int64
			var strTalker, strContent string
			var compressContent, bytesExtra []byte

			err := rows.Scan(&localId, &msgSvrID, &msgType, &subType, &isSender, &createTime,
				&strTalker, &strContent, &compressContent, &bytesExtra)
			if err != nil {
				log.Printf("扫描消息失败: %v", err)
				continue
			}

			msg.LocalId = localId
			msg.MsgSvrId = strconv.FormatInt(int64(msgSvrID), 10)
			msg.Type = msgType
			msg.SubType = subType
			msg.IsSender = isSender
			msg.CreateTime = createTime
			msg.Talker = strTalker
			msg.Content = strContent
			msg.IsChatRoom = strings.HasSuffix(strTalker, "@chatroom")
			msg.bytesExtra = bytesExtra

			// 解析BytesExtra获取文件路径
			ce.parseBytesExtra(&msg)

			allMessages = append(allMessages, msg)
		}
		rows.Close()
	}

	return allMessages, nil
}

// 提取聊天记录
func (ce *ChatExtractor) ExtractChatHistory() ([]ChatSession, error) {
	messages, err := ce.GetAllMessages()
	if err != nil {
		return nil, fmt.Errorf("获取消息失败: %v", err)
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("未找到与 %s 的聊天记录", ce.TargetWxId)
	}

	// 获取用户昵称
	selfNickName, err := ce.GetUserInfo(ce.SelfWxId)
	if err != nil {
		log.Printf("获取自己的昵称失败，使用微信ID: %s", ce.SelfWxId)
		selfNickName = ce.SelfWxId
	}
	
	targetNickName, err := ce.GetUserInfo(ce.TargetWxId)
	if err != nil {
		log.Printf("获取目标用户昵称失败，使用微信ID: %s", ce.TargetWxId)
		targetNickName = ce.TargetWxId
	}

	// 创建对话记录
	var dialogues []Dialogue
	for index, msg := range messages {
		var speaker string
		var text string
		
		if msg.IsChatRoom {
			// 群聊消息：从BytesExtra中解析发送者信息
			// 先打印原始消息内容用于调试
			fmt.Printf("DEBUG: 群聊原始消息 - IsSender: %d, StrContent: '%s', UserInfo.UserName: '%s'\n", msg.IsSender, msg.Content, msg.UserInfo.UserName)
			text = ce.GetMessageText(msg)
			speaker = ce.parseGroupMessageSender(msg, msg.IsSender == 1, selfNickName, targetNickName)
		} else {
			// 私聊消息：根据IsSender判断
			if msg.IsSender == 1 {
				speaker = selfNickName
			} else {
				speaker = targetNickName
			}
			text = ce.GetMessageText(msg)
		}

		timeStr := formatTime(msg.CreateTime)

		dialogues = append(dialogues, Dialogue{
			Index:   index + 1, // 序号从1开始
			Speaker: speaker,
			Text:    text,
			Time:    timeStr,
		})
	}

	// 创建聊天会话
	session := ChatSession{
		Instruction: fmt.Sprintf("与 %s 的聊天记录", targetNickName),
		Dialogue:    dialogues,
	}

	return []ChatSession{session}, nil
}

// 获取所有联系人
func getAllContacts(microMsgDB *sql.DB, dataPath string) ([]Contact, error) {
	var contacts []Contact

	// 从Contact表获取所有联系人
	query := "SELECT UserName, NickName FROM Contact WHERE UserName != ''"
	rows, err := microMsgDB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("查询联系人失败: %v", err)
	}
	defer rows.Close()

	contactMap := make(map[string]Contact)

	for rows.Next() {
		var contact Contact
		err := rows.Scan(&contact.UserName, &contact.NickName)
		if err != nil {
			log.Printf("扫描联系人失败: %v", err)
			continue
		}
		contactMap[contact.UserName] = contact
	}

	// 从MSG数据库统计消息数量
	msgDir := filepath.Join(dataPath, "Msg", "Multi")
	
	// 查找所有MSG*.db文件
	for i := 0; ; i++ {
		var msgPath string
		if i == 0 {
			// 先尝试MSG.db，再尝试MSG0.db
			msgPath = filepath.Join(msgDir, "MSG.db")
			if _, err := os.Stat(msgPath); os.IsNotExist(err) {
				msgPath = filepath.Join(msgDir, "MSG0.db")
			}
		} else {
			msgPath = filepath.Join(msgDir, fmt.Sprintf("MSG%d.db", i))
		}

		if _, err := os.Stat(msgPath); os.IsNotExist(err) {
			break
		}

		db, err := sql.Open("sqlite3", msgPath)
		if err != nil {
			log.Printf("打开%s失败: %v", msgPath, err)
			continue
		}

		// 统计每个用户的消息数量
		query := "SELECT StrTalker, COUNT(*) as count FROM MSG WHERE StrTalker != '' GROUP BY StrTalker"
		rows, err := db.Query(query)
		if err != nil {
			log.Printf("查询%s中的消息统计失败: %v", msgPath, err)
			db.Close()
			continue
		}

		for rows.Next() {
			var userName string
			var count int
			err := rows.Scan(&userName, &count)
			if err != nil {
				log.Printf("扫描消息统计失败: %v", err)
				continue
			}

			if contact, exists := contactMap[userName]; exists {
				contact.MessageCount += count
				contactMap[userName] = contact
			} else {
				// 如果联系人表中没有，创建一个新的
				contactMap[userName] = Contact{
					UserName: userName,
					NickName: "",
					MessageCount: count,
				}
			}
		}
		rows.Close()
		db.Close()
	}

	// 转换为切片
	for _, contact := range contactMap {
		if contact.MessageCount > 0 {
			contacts = append(contacts, contact)
		}
	}

	return contacts, nil
}

// 用户选择聊天对象（支持多选）
func selectContacts(contacts []Contact) ([]Contact, error) {
	// 按消息数量排序
	sort.Slice(contacts, func(i, j int) bool {
		return contacts[i].MessageCount > contacts[j].MessageCount
	})

	fmt.Printf("\n找到 %d 个聊天对象:\n", len(contacts))
	fmt.Println("=" + strings.Repeat("=", 80))
	fmt.Printf("%-5s %-30s %-20s %-10s\n", "序号", "微信ID", "昵称", "消息数量")
	fmt.Println("-" + strings.Repeat("-", 80))

	for i, contact := range contacts {
		nickName := contact.NickName
		if nickName == "" {
			nickName = "未知"
		}
		fmt.Printf("%-5d %-30s %-20s %-10d\n", i+1, contact.UserName, nickName, contact.MessageCount)
	}

	fmt.Print("\n请选择聊天对象序号 (支持多选，用逗号分隔，如: 1,3,5): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return []Contact{}, fmt.Errorf("请输入至少一个序号")
	}

	// 解析输入的序号
	var selectedContacts []Contact
	parts := strings.Split(input, ",")
	
	for _, part := range parts {
		part = strings.TrimSpace(part)
		index, err := strconv.Atoi(part)
		if err != nil || index < 1 || index > len(contacts) {
			return []Contact{}, fmt.Errorf("无效的序号: %s", part)
		}
		
		// 检查是否重复选择
		alreadySelected := false
		for _, selected := range selectedContacts {
			if selected.UserName == contacts[index-1].UserName {
				alreadySelected = true
				break
			}
		}
		
		if !alreadySelected {
			selectedContacts = append(selectedContacts, contacts[index-1])
		}
	}

	if len(selectedContacts) == 0 {
		return []Contact{}, fmt.Errorf("未选择任何有效的聊天对象")
	}

	return selectedContacts, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("使用方法: go run chat_extractor_simple.go <数据路径>")
		fmt.Println("示例: go run chat_extractor_simple.go ./build/bin/User/wxid_4mqdhcc7689o22")
		os.Exit(1)
	}

	dataPath := os.Args[1]

	// 检查数据路径是否存在
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		log.Fatalf("数据路径不存在: %s", dataPath)
	}

	// 打开MicroMsg.db
	microMsgPath := filepath.Join(dataPath, "Msg", "MicroMsg.db")
	microMsgDB, err := sql.Open("sqlite3", microMsgPath)
	if err != nil {
		log.Fatalf("打开MicroMsg.db失败: %v", err)
	}
	defer microMsgDB.Close()

	// 获取所有联系人
	contacts, err := getAllContacts(microMsgDB, dataPath)
	if err != nil {
		log.Fatalf("获取联系人失败: %v", err)
	}

	if len(contacts) == 0 {
		log.Fatalf("未找到任何聊天对象")
	}

	// 用户选择聊天对象
	selectedContacts, err := selectContacts(contacts)
	if err != nil {
		log.Fatalf("选择聊天对象失败: %v", err)
	}

	// 获取自己的wxid（假设是数据路径中的最后一个目录名）
	// 使用filepath.Base来正确提取最后一个路径组件
	selfWxId := filepath.Base(dataPath)
	
	// 确保selfWxId是有效的微信ID格式
	if !strings.HasPrefix(selfWxId, "wxid_") {
		log.Fatalf("无法从路径中提取有效的微信ID: %s，提取到的值: %s", dataPath, selfWxId)
	}

	fmt.Printf("\n数据路径: %s\n", dataPath)
	fmt.Printf("检测到的用户微信ID: %s\n", selfWxId)
	fmt.Printf("已选择 %d 个聊天对象\n", len(selectedContacts))

	// 创建data目录
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("创建data目录失败: %v", err)
	}

	// 获取自己的昵称
	var selfNickName string
	// 先尝试从第一个联系人获取自己的昵称
	if len(selectedContacts) > 0 {
		tempExtractor, err := NewChatExtractor(dataPath, selfWxId, selectedContacts[0].UserName)
		if err == nil {
			selfNickName, err = tempExtractor.GetUserInfo(selfWxId)
			if err != nil {
				log.Printf("获取自己的昵称失败，使用微信ID: %s", selfWxId)
				selfNickName = selfWxId
			}
			tempExtractor.Close()
		} else {
			selfNickName = selfWxId
		}
	} else {
		selfNickName = selfWxId
	}

	// 清理自己的昵称中的特殊字符，避免文件名问题
	selfNickName = strings.ReplaceAll(selfNickName, "/", "_")
	selfNickName = strings.ReplaceAll(selfNickName, "\\", "_")
	selfNickName = strings.ReplaceAll(selfNickName, ":", "_")
	selfNickName = strings.ReplaceAll(selfNickName, "*", "_")
	selfNickName = strings.ReplaceAll(selfNickName, "?", "_")
	selfNickName = strings.ReplaceAll(selfNickName, "\"", "_")
	selfNickName = strings.ReplaceAll(selfNickName, "<", "_")
	selfNickName = strings.ReplaceAll(selfNickName, ">", "_")
	selfNickName = strings.ReplaceAll(selfNickName, "|", "_")

	// 为每个选中的联系人提取聊天记录
	for i, selectedContact := range selectedContacts {
		fmt.Printf("\n正在处理第 %d/%d 个联系人: %s (%s)\n", i+1, len(selectedContacts), selectedContact.NickName, selectedContact.UserName)

		// 创建提取器
		extractor, err := NewChatExtractor(dataPath, selfWxId, selectedContact.UserName)
		if err != nil {
			log.Printf("创建提取器失败 (联系人: %s): %v", selectedContact.NickName, err)
			continue
		}

		// 提取聊天记录
		sessions, err := extractor.ExtractChatHistory()
		if err != nil {
			log.Printf("提取聊天记录失败 (联系人: %s): %v", selectedContact.NickName, err)
			extractor.Close()
			continue
		}

		// 输出JSON
		jsonData, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			log.Printf("序列化JSON失败 (联系人: %s): %v", selectedContact.NickName, err)
			extractor.Close()
			continue
		}

		// 获取时间范围
		var startTime, endTime time.Time
		if len(sessions) > 0 && len(sessions[0].Dialogue) > 0 {
			startTime, _ = time.Parse("2006-01-02 15:04:05", sessions[0].Dialogue[0].Time)
			endTime, _ = time.Parse("2006-01-02 15:04:05", sessions[0].Dialogue[len(sessions[0].Dialogue)-1].Time)
		}

		// 获取目标用户昵称
		targetNickName, err := extractor.GetUserInfo(extractor.TargetWxId)
		if err != nil {
			log.Printf("获取目标用户昵称失败，使用微信ID: %s", extractor.TargetWxId)
			targetNickName = selectedContact.NickName
			if targetNickName == "" {
				targetNickName = "对方"
			}
		}

		// 格式化时间 - 使用更简洁的格式
		startTimeStr := startTime.Format("2006_1_2")
		endTimeStr := endTime.Format("2006_1_2")
		
		// 清理目标用户昵称中的特殊字符，避免文件名问题
		targetNickName = strings.ReplaceAll(targetNickName, "/", "_")
		targetNickName = strings.ReplaceAll(targetNickName, "\\", "_")
		targetNickName = strings.ReplaceAll(targetNickName, ":", "_")
		targetNickName = strings.ReplaceAll(targetNickName, "*", "_")
		targetNickName = strings.ReplaceAll(targetNickName, "?", "_")
		targetNickName = strings.ReplaceAll(targetNickName, "\"", "_")
		targetNickName = strings.ReplaceAll(targetNickName, "<", "_")
		targetNickName = strings.ReplaceAll(targetNickName, ">", "_")
		targetNickName = strings.ReplaceAll(targetNickName, "|", "_")
		
		// 生成文件名：我的昵称_聊天对象的昵称聊天记录开始时间_聊天记录结束时间
		outputFile := filepath.Join(dataDir, fmt.Sprintf("%s_%s%s_%s.json", 
			selfNickName, targetNickName, startTimeStr, endTimeStr))
		
		err = os.WriteFile(outputFile, jsonData, 0644)
		if err != nil {
			log.Printf("保存文件失败 (联系人: %s): %v", selectedContact.NickName, err)
		} else {
			fmt.Printf("聊天记录已保存到: %s\n", outputFile)
		}

		extractor.Close()
	}

	fmt.Printf("\n处理完成！共处理了 %d 个联系人的聊天记录。\n", len(selectedContacts))
}

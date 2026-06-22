package video

import "regexp"

/*一个视频可以有多个标签
一个标签也可以属于多个视频
这就是典型的多对多关系。
多对多在数据库里通常要拆成三张表：
videos
tags
video_tags*/

type Tag struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;type:varchar(100);not null" json:"name"`
}

// video_tags 是中间表
type VideoTag struct {
	ID      uint `gorm:"primaryKey"`
	VideoID uint `gorm:"index;not null"`
	TagID   uint `gorm:"index;not null"`
}

// 从字符串里找出 #xxx 形式的标签,()表示：捕获的是去掉#的内容
var tagRegex = regexp.MustCompile(`#([\p{L}\p{N}_]+)`)

// 去重tag
func ExtractTags(text string) []string {
	matches := tagRegex.FindAllStringSubmatch(text, -1)
	//这个 map 用来去重，value默认是false
	seen := make(map[string]bool)
	var tags []string

	for _, m := range matches {
		tag := m[1]
		//如果是新的tag，那么seen[tag]=false
		if !seen[tag] {
			seen[tag] = true //置位true，代表已经存在了
			tags = append(tags, tag)
		}
	}

	return tags
}

package main

import "github.com/wailsapp/wails/v3/pkg/application"

type menuLabel struct {
	item        *application.MenuItem
	key, suffix string
}

func (u *UI) localizedItem(item *application.MenuItem, key, suffix string) *application.MenuItem {
	u.menuLabels = append(u.menuLabels, menuLabel{item, key, suffix})
	return item
}

func (u *UI) text(key string) string {
	locale, _ := u.locale.Load().(string)
	translations := map[string]map[string]string{
		"en":    {"复制会话 ID": "Copy session ID", "关于 ": "About ", "设置…": "Settings…", "隐藏 ": "Hide ", "隐藏其他": "Hide Others", "退出 ": "Quit "},
		"zh-TW": {"复制会话 ID": "複製對話 ID", "关于 ": "關於 ", "设置…": "設定…", "隐藏 ": "隱藏 ", "隐藏其他": "隱藏其他", "退出 ": "結束 "},
		"ja":    {"复制会话 ID": "セッション ID をコピー", "关于 ": "情報：", "设置…": "設定…", "隐藏 ": "隠す：", "隐藏其他": "ほかを隠す", "退出 ": "終了："},
		"de":    {"复制会话 ID": "Sitzungs-ID kopieren", "关于 ": "Über ", "设置…": "Einstellungen…", "隐藏 ": "Ausblenden: ", "隐藏其他": "Andere ausblenden", "退出 ": "Beenden: "},
	}
	if value := translations[locale][key]; value != "" {
		return value
	}
	return key
}

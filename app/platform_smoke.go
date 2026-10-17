package main

// Opt-in native WebView verification. The result file is the test contract: a
// successful process exit without {"passed":true} is not a passing smoke test.
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func validatePlatformSmoke() error {
	sandbox := os.Getenv("KOTO_APP_SANDBOX")
	if sandbox == "" || !filepath.IsAbs(sandbox) {
		return fmt.Errorf("platform smoke requires an absolute KOTO_APP_SANDBOX")
	}
	if _, err := os.Stat(filepath.Join(sandbox, ".koto-sandbox")); err != nil {
		return fmt.Errorf("platform smoke requires synthetic fixtures from cmd/seed: %w", err)
	}
	return nil
}

func platformSmoke(resultFile string, ui *UI, core *Core, marks <-chan string) {
	result := map[string]any{"passed": false, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"sandbox": os.Getenv("KOTO_APP_SANDBOX"), "started": time.Now().UTC().Format(time.RFC3339)}
	defer func() {
		if failure := recover(); failure != nil {
			result["error"] = fmt.Sprint(failure)
		}
		result["finished"] = time.Now().UTC().Format(time.RFC3339)
		data, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(resultFile, append(data, '\n'), 0600); err != nil {
			log.Printf("platform smoke result write failed: %v", err)
		}
		log.Printf("platform smoke: %s", data)
		ui.app.Quit()
	}()
	if core.Status().State != "ready" {
		panic("core did not become ready: " + core.Status().Error)
	}
	ui.main.Show()
	ready := time.NewTimer(45 * time.Second)
	defer ready.Stop()
waitReady:
	for {
		select {
		case mark := <-marks:
			if mark == "ready" {
				break waitReady
			}
		case <-ready.C:
			panic("native WebView did not report ready")
		}
	}
	result["webviewReady"] = true
	step := 0
	js := func(code string) string {
		step++
		prefix := fmt.Sprintf("platform-smoke:%d:", step)
		encoded, _ := json.Marshal(code)
		marker, _ := json.Marshal(prefix)
		ui.main.ExecJS(`(async()=>{let result;try{result='ok:'+String(await(0,eval)(` + string(encoded) + `))}catch(e){result='error:'+String(e)+' DOM:'+JSON.stringify({title:document.querySelector('.session-heading h1')?.textContent,selected:document.querySelector('#sessions [aria-selected="true"]')?.style.getPropertyValue('--custom-contextmenu-data'),rows:[...document.querySelectorAll('#sessions [role=option]')].slice(0,20).map(e=>({id:e.style.getPropertyValue('--custom-contextmenu-data'),text:e.querySelector('.t-medium')?.textContent})),sheet:document.querySelector('.sheet')?.textContent})}await fetch('/wails/runtime',{method:'POST',headers:{'Content-Type':'application/json','x-wails-client-id':'koto-platform-smoke'},body:JSON.stringify({object:0,method:0,args:{methodID:2524806435,'call-id':` + string(marker) + `,args:[` + string(marker) + `+result]}})})})()`)
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for {
			select {
			case mark := <-marks:
				if strings.HasPrefix(mark, prefix) {
					value := strings.TrimPrefix(mark, prefix)
					if !strings.HasPrefix(value, "ok:") {
						panic(value + " in " + code)
					}
					return strings.TrimPrefix(value, "ok:")
				}
			case <-timer.C:
				panic("WebView JavaScript timed out: " + code)
			}
		}
	}
	js(`window.platformWait=async(test)=>{const end=Date.now()+12000;while(Date.now()<end){if(test())return;await new Promise(r=>setTimeout(r,100))}throw Error('UI condition timed out')}`)
	js(`(async()=>{window.platformNav=(text)=>[...document.querySelectorAll('nav [role=option]')].find(e=>e.textContent.trim()===text);await window.platformWait(()=>window.platformNav('会话复制'));if(window.platformNav('会话复制').getAttribute('aria-expanded')!=='true')window.platformNav('会话复制').click();await window.platformWait(()=>window.platformNav('全部会话'));window.platformNav('全部会话').click();await window.platformWait(()=>window.platformNav('全部会话')?.getAttribute('aria-selected')==='true');})()`)
	js(`(async()=>{window.platformSource=()=>[...document.querySelectorAll('#sessions [role=option]')].find(e=>e.style.getPropertyValue('--custom-contextmenu-data').trim()==='claude-code:6b1f2a40-1c2d-4e3f-8a4b-5c6d7e8f9a01');await window.platformWait(window.platformSource);window.platformSource().click();})()`)
	js(`window.platformWait(()=>window.platformSource()?.getAttribute('aria-selected')==='true'&&document.querySelector('.session-heading h1')?.textContent==='把 CAS 打包改成按天分片'&&document.getElementById('copy-main')&&!document.getElementById('copy-main').disabled)`)
	sourceID := js(`document.querySelector('#sessions [aria-selected="true"]').style.getPropertyValue('--custom-contextmenu-data').trim()`)
	js(`document.getElementById('copy-main').click()`)
	js(`window.platformWait(()=>document.querySelector('.copy-range'))`)
	result["selectionAtStart"] = js(`JSON.stringify({range:document.querySelector('.copy-range')?.textContent,buttonText:document.getElementById('copy-main')?.textContent,disabled:document.getElementById('copy-main')?.disabled,checked:document.querySelectorAll('.message-check input:checked').length})`)
	js(`if(document.querySelector('.copy-range')?.textContent!=='全部 5 轮'||document.getElementById('copy-main').disabled)throw Error('copy selection did not default to all complete turns')`)
	result["selectionDefaultAll"] = true
	js(`window.platformScrollBefore=[...document.querySelectorAll('.scroll')].map(el=>({el,style:el.getAttribute('style'),top:el.scrollTop,width:el.clientWidth-parseFloat(getComputedStyle(el).paddingLeft)-parseFloat(getComputedStyle(el).paddingRight)}))`)
	js(`document.getElementById('copy-main').click()`)
	js(`(async()=>{const target=()=>[...document.querySelectorAll('.sheet [role=option]')].find(e=>e.querySelector('.grow')?.textContent.trim().toLowerCase()==='pi');await window.platformWait(target);target().click();await window.platformWait(()=>target()?.getAttribute('aria-selected')==='true'&&document.querySelector('.sheet-foot .btn-primary:not(:disabled)'));})()`)
	js(`for(const before of window.platformScrollBefore){const style=getComputedStyle(before.el);if(style.overflowY!=='hidden')throw Error('dialog background is still scrollable');const width=before.el.clientWidth-parseFloat(style.paddingLeft)-parseFloat(style.paddingRight);if(Math.abs(width-before.width)>1)throw Error('opening dialog changed background content width')}
const body=document.querySelector('.sheet-body');if(!body||getComputedStyle(body).overflowY!=='auto')throw Error('dialog content scrolling was locked');const height=body.style.maxHeight;body.style.maxHeight='80px';body.scrollTop=32;if(body.scrollTop===0)throw Error('dialog content cannot scroll');body.scrollTop=0;body.style.maxHeight=height;
document.querySelector('.sheet-foot button').click()`)
	js(`(async()=>{await window.platformWait(()=>!document.querySelector('.sheet'));for(const before of window.platformScrollBefore){if((before.el.getAttribute('style')??'')!==(before.style??''))throw Error('dialog did not restore background styles');if(Math.abs(before.el.scrollTop-before.top)>1)throw Error('dialog changed background scroll position')}document.getElementById('copy-main').click();const target=()=>[...document.querySelectorAll('.sheet [role=option]')].find(e=>e.querySelector('.grow')?.textContent.trim().toLowerCase()==='pi');await window.platformWait(target);target().click();await window.platformWait(()=>target()?.getAttribute('aria-selected')==='true'&&document.querySelector('.sheet-foot .btn-primary:not(:disabled)'));})()`)
	result["modalScrollIsolation"] = true
	js(`document.querySelector('.sheet-foot .btn-primary').click()`)
	js(`(async()=>{await window.platformWait(()=>[...document.querySelectorAll('.sheet button')].some(e=>e.textContent.includes('查看新会话')));[...document.querySelectorAll('.sheet button')].find(e=>e.textContent.includes('查看新会话')).click();await window.platformWait(()=>!document.querySelector('.sheet')&&document.querySelector('#sessions [aria-selected="true"]'));})()`)
	destinationID := js(`document.querySelector('#sessions [aria-selected="true"]').style.getPropertyValue('--custom-contextmenu-data').trim()`)
	if sourceID == destinationID || !strings.HasPrefix(destinationID, "pi:") {
		panic("UI did not select a new pi session: " + destinationID)
	}
	thread, err := core.API().Thread(context.Background(), destinationID, 0, "")
	if err != nil {
		panic(fmt.Sprintf("native session readback: %v", err))
	}
	users, answers := 0, 0
	for _, event := range thread.Events {
		if event.Kind == "message" && event.Actor == "user" {
			users++
		}
		if event.Kind == "message" && event.Actor == "assistant" {
			answers++
		}
	}
	if users == 0 || answers == 0 {
		panic("native readback has no conversation messages")
	}
	result["source"] = sourceID
	result["destination"] = destinationID
	result["userMessages"] = users
	result["assistantMessages"] = answers
	result["nativeCopyReadback"] = true
	destinationJSON, _ := json.Marshal(destinationID)
	js(`window.platformDestination=` + string(destinationJSON) + `;window.platformDestinationTitle=document.querySelector('.session-heading h1')?.textContent;document.querySelector('button[aria-label="移到回收站"]').click()`)
	js(`(async()=>{await window.platformWait(()=>document.getElementById('trash-go'));document.getElementById('trash-go').click();await window.platformWait(()=>![...document.querySelectorAll('#sessions [role=option]')].some(e=>e.style.getPropertyValue('--custom-contextmenu-data').trim()===window.platformDestination));await new Promise(r=>setTimeout(r,800));if([...document.querySelectorAll('#sessions [role=option]')].some(e=>e.style.getPropertyValue('--custom-contextmenu-data').trim()===window.platformDestination))throw Error('deleted copy reappeared');if(document.querySelector('#sessions [aria-selected="true"]')?.style.getPropertyValue('--custom-contextmenu-data').trim()===window.platformDestination)throw Error('deleted copy remains selected');if(window.platformDestinationTitle&&document.querySelector('.session-heading h1')?.textContent===window.platformDestinationTitle)throw Error('deleted copy detail remains visible');})()`)
	js(`(async()=>{[...document.querySelectorAll('nav [role=option]')].find(e=>e.textContent.includes('回收站')).click();await window.platformWait(()=>document.querySelector('[role=list][aria-label="回收站"] [role=listitem]'));const row=[...document.querySelectorAll('[role=list][aria-label="回收站"] [role=listitem]')].find(e=>e.textContent.includes('把 CAS 打包改成按天分片'));if(!row)throw Error('copied session absent from trash');row.querySelector('button').click();await window.platformWait(()=>document.querySelector('#sessions [aria-selected="true"]')?.style.getPropertyValue('--custom-contextmenu-data').trim()===window.platformDestination);await window.platformWait(()=>document.getElementById('copy-main'));})()`)
	if _, err := core.API().Thread(context.Background(), destinationID, 0, ""); err != nil {
		panic(fmt.Sprintf("restored native session readback: %v", err))
	}
	result["trashRestore"] = true
	js(`window.platformNav('设置').click()`)
	js(`(async()=>{const tab=()=>[...document.querySelectorAll('[role=tab]')].find(e=>e.textContent.trim()==='通用');await window.platformWait(tab);tab().click()})()`)
	versionJSON, _ := json.Marshal(strings.TrimSpace(appVersionFile))
	js(`window.platformWait(()=>document.body.textContent.includes(` + string(versionJSON) + `)&&document.body.textContent.includes('github.com/Seanium/Koto'))`)
	result["appInfo"] = strings.TrimSpace(appVersionFile)
	result["passed"] = true
}

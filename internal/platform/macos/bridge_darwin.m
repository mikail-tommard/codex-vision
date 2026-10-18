#include <stdatomic.h>
//go:build darwin && cgo
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ImageIO/ImageIO.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <objc/message.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <unistd.h>
#include <dlfcn.h>
#include "bridge.h"
#include "json_bool.h"
#include "focus_resolver.h"
#include "window_match.h"
#include "scroll_target.h"

static char *json(id obj) {
 NSError *err=nil; NSData *data=[NSJSONSerialization dataWithJSONObject:obj options:0 error:&err];
 if(!data)return strdup("{\"error\":\"could not serialize native result\"}");
 char *out=malloc(data.length+1);if(!out)return NULL;memcpy(out,data.bytes,data.length);out[data.length]=0;return out;
}
static int fail(char **error, NSString *reason) { *error=strdup(reason.UTF8String);return 0; }
// A missing value is not the same as false. Public console/login flags are
// required; the lock key is optional and normally absent while unlocked.
static int boolValue(CFDictionaryRef dict, CFStringRef key, int absent) {
 CFTypeRef value=CFDictionaryGetValue(dict,key);if(!value)return absent;
 if(CFGetTypeID(value)==CFBooleanGetTypeID())return CFBooleanGetValue((CFBooleanRef)value)?1:0;
 if(CFGetTypeID(value)==CFNumberGetTypeID()){int n=0;if(CFNumberGetValue((CFNumberRef)value,kCFNumberIntType,&n)&&(n==0||n==1))return n;}
 return -1;
}
void cv_read_permissions(cv_permission_state *s) { @autoreleasepool {
 memset(s,0,sizeof(*s));s->on_console=-1;s->login_done=-1;s->screen_locked=-1;
 s->effective_uid=(unsigned int)geteuid();
 // Correct native ABI: Boolean return is normalized to int at this C boundary.
 // Never cache these answers in the application; reread on every diagnostic/action.
 s->accessibility_permission=AXIsProcessTrusted()?1:0;
 s->post_event_permission=CGPreflightPostEventAccess()?1:0;
 s->screen_capture_permission=CGPreflightScreenCaptureAccess()?1:0;
 CFDictionaryRef session=CGSessionCopyCurrentDictionary();if(!session)return;
 s->session_available=1;
 s->on_console=boolValue(session,kCGSessionOnConsoleKey,-1);
 s->login_done=boolValue(session,kCGSessionLoginDoneKey,-1);
 s->lock_indicator_present=CFDictionaryContainsKey(session,CFSTR("CGSSessionScreenIsLocked"))?1:0;
 s->screen_locked=boolValue(session,CFSTR("CGSSessionScreenIsLocked"),0);
 CFTypeRef uid=CFDictionaryGetValue(session,kCGSessionUserIDKey);int64_t n=-1;
 if(uid&&CFGetTypeID(uid)==CFNumberGetTypeID()&&CFNumberGetValue((CFNumberRef)uid,kCFNumberSInt64Type,&n)&&n>=0&&n<=UINT32_MAX){s->session_uid_known=1;s->session_uid=(unsigned int)n;}
 CFRelease(session);
}}
void cv_request_accessibility(void) { @autoreleasepool {
 // kAXTrustedCheckOptionPrompt requires CFBoolean, not a C bool pointer.
 NSDictionary *options=@{(__bridge NSString *)kAXTrustedCheckOptionPrompt:@YES};
 // The system prompt is asynchronous. This call does not grant access.
 (void)AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options);
}}
static const char *consoleFailure(const cv_permission_state *s) {
 if(!s->session_available)return "console_session_unavailable: CGSessionCopyCurrentDictionary returned NULL; this process has no Quartz GUI session or WindowServer access";
 if(s->on_console<0)return "console_state_unknown: on-console flag missing or invalid";
 if(!s->on_console)return "console_not_active: this process is not attached to the active console session";
 if(s->login_done<0)return "console_login_state_unknown: login-complete flag missing or invalid";
 if(!s->login_done)return "console_login_incomplete: GUI login has not completed";
 if(!s->session_uid_known)return "console_user_unknown: session user ID unavailable";
 if(s->session_uid!=s->effective_uid)return "console_user_mismatch: GUI session belongs to another effective user";
 if(s->screen_locked<0)return "console_lock_state_unknown: lock indicator has an invalid type";
 if(s->screen_locked)return "console_locked: the active GUI session is locked";
 return NULL;
}
static int requireConsole(char **error) {
 cv_permission_state s;cv_read_permissions(&s);const char *reason=consoleFailure(&s);if(reason)return fail(error,@(reason));return 1;
}
const char *cv_permission_failure(const cv_permission_state *s,int checks) {
 const char *reason=consoleFailure(s);if(reason)return reason;
 if(checks>=1&&!s->accessibility_permission)return "accessibility_permission_denied: AXIsProcessTrusted returned false for this MCP process; call macos_permission_diagnostics with request_accessibility=true and grant the identity shown by macOS";
 if(checks>=2&&!s->post_event_permission)return "event_post_permission_denied: CGPreflightPostEventAccess returned false; this MCP process is not authorized to synthesize input events";
 return NULL;
}
static int requireAccessibility(char **error,int postEvents) {
 cv_permission_state s;cv_read_permissions(&s);const char *reason=cv_permission_failure(&s,postEvents?2:1);if(reason)return fail(error,@(reason));return 1;
}
void cv_request_screen_capture(void) { (void)CGRequestScreenCaptureAccess(); }
char *cv_process_identity(void) { @autoreleasepool {
 NSMutableArray *chain=[NSMutableArray array];pid_t current=getpid();
 for(int depth=0;depth<16&&current>0;depth++){
 char path[PROC_PIDPATHINFO_MAXSIZE]={0};int pathResult=proc_pidpath(current,path,sizeof(path));
 struct proc_bsdinfo info={0};int got=proc_pidinfo(current,PROC_PIDTBSDINFO,0,&info,sizeof(info));
 NSRunningApplication *app=[NSRunningApplication runningApplicationWithProcessIdentifier:current];
 NSMutableDictionary *item=[@{@"pid":@(current),@"executable_path":pathResult>0?@(path):@"",@"bundle_id":app.bundleIdentifier?:@"",@"bundle_path":app.bundleURL.path?:@"",@"parent_pid_known":got==sizeof(info)?@YES:@NO} mutableCopy];
 pid_t parent=got==sizeof(info)?(pid_t)info.pbi_ppid:0;if(got==sizeof(info))item[@"parent_pid"]=@(parent);
 [chain addObject:item];if(parent<=0||parent==current)break;current=parent;
 }
 return json(@{@"process_chain":chain,@"process_chain_limit":@16,@"bundle_id":NSBundle.mainBundle.bundleIdentifier?:@"",@"bundle_path":NSBundle.mainBundle.bundlePath?:@"",@"os_version":NSProcessInfo.processInfo.operatingSystemVersionString,@"sdk":@(__MAC_OS_X_VERSION_MAX_ALLOWED)});
}}
static NSDictionary *rect(CGRect b) {return @{@"x":@((int)b.origin.x),@"y":@((int)b.origin.y),@"width":@((int)b.size.width),@"height":@((int)b.size.height)};}
char *cv_monitors(void) { @autoreleasepool {
 char *reason=NULL;if(!requireConsole(&reason)){char *out=json(@{@"error":@(reason)});free(reason);return out;}
 CGDirectDisplayID displays[64];uint32_t count=0;if(CGGetActiveDisplayList(64,displays,&count)!=kCGErrorSuccess)return json(@{@"error":@"CGGetActiveDisplayList failed"});
 NSMutableArray *out=[NSMutableArray array];for(uint32_t i=0;i<count;i++) {CGRect b=CGDisplayBounds(displays[i]);[out addObject:@{@"id":[NSString stringWithFormat:@"%u",displays[i]],@"bounds":rect(b),@"primary":(displays[i]==CGMainDisplayID()?@YES:@NO),@"pixel_width":@(CGDisplayPixelsWide(displays[i])),@"pixel_height":@(CGDisplayPixelsHigh(displays[i]))}];}return json(out);
}}
// Discovery/focus include windows on other Spaces and minimized windows.
// Active-window checks and pointer hit tests must stay limited to this screen.
static NSArray *windowInfo(void) {return CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID));}
static NSArray *allWindowInfo(void) {return CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionAll|kCGWindowListExcludeDesktopElements,kCGNullWindowID));}
// Do not trust NSWorkspace's cached foreground in a command-line server.
// GetFrontProcess is a synchronous legacy process-manager query used only when
// AX explicitly reports NoValue. The fallback still requires app-specific AX
// window bounds, stable foreground PID and a unique visible Quartz match.
static int freshFrontPID(void *ctx,int *pid) {
 (void)ctx;ProcessSerialNumber front={0,0};pid_t value=0;
 OSErr e=GetFrontProcess(&front);if(e!=noErr)return e;
 e=GetProcessPID(&front,&value);if(e!=noErr)return e;
 *pid=(int)value;return value>0?0:-1;
}
static int systemFocusedPID(void *ctx,int *pid) {
 (void)ctx;AXUIElementRef system=AXUIElementCreateSystemWide();CFTypeRef app=NULL;
 AXUIElementSetMessagingTimeout(system,0.5);
 AXError e=AXUIElementCopyAttributeValue(system,kAXFocusedApplicationAttribute,&app);CFRelease(system);
 if(e==kAXErrorSuccess&&(!app||CFGetTypeID(app)!=AXUIElementGetTypeID()))e=kAXErrorNoValue;
 pid_t value=0;if(e==kAXErrorSuccess)e=AXUIElementGetPid((AXUIElementRef)app,&value);
 if(app)CFRelease(app);*pid=(int)value;return e;
}
static int appFocusedBounds(void *ctx,int pid,cv_focus_rect *bounds) {
 (void)ctx;AXUIElementRef app=AXUIElementCreateApplication((pid_t)pid);CFTypeRef window=NULL,position=NULL,size=NULL;
 AXUIElementSetMessagingTimeout(app,0.5);
 AXError e=AXUIElementCopyAttributeValue(app,kAXFocusedWindowAttribute,&window);CFRelease(app);
 if(e==kAXErrorSuccess&&(!window||CFGetTypeID(window)!=AXUIElementGetTypeID()))e=kAXErrorNoValue;
 pid_t owner=0;if(e==kAXErrorSuccess)e=AXUIElementGetPid((AXUIElementRef)window,&owner);
 if(e==kAXErrorSuccess&&owner!=(pid_t)pid)e=kAXErrorInvalidUIElement;
 if(e==kAXErrorSuccess)e=AXUIElementCopyAttributeValue((AXUIElementRef)window,kAXPositionAttribute,&position);
 if(e==kAXErrorSuccess)e=AXUIElementCopyAttributeValue((AXUIElementRef)window,kAXSizeAttribute,&size);
 CGPoint point=CGPointZero;CGSize extent=CGSizeZero;
 BOOL valid=e==kAXErrorSuccess&&position&&size&&CFGetTypeID(position)==AXValueGetTypeID()&&CFGetTypeID(size)==AXValueGetTypeID()&&AXValueGetValue((AXValueRef)position,kAXValueCGPointType,&point)&&AXValueGetValue((AXValueRef)size,kAXValueCGSizeType,&extent);
 if(position)CFRelease(position);if(size)CFRelease(size);if(window)CFRelease(window);
 if(!valid)return e==kAXErrorSuccess?kAXErrorNoValue:e;
 *bounds=(cv_focus_rect){point.x,point.y,extent.width,extent.height};return 0;
}
static int focusedWindow(pid_t *pid, CGRect *bounds, char **error) {
 cv_focus_readers reads={systemFocusedPID,freshFrontPID,appFocusedBounds};cv_focus_result result;
 if(!cv_resolve_focus(&reads,NULL,&result)){
  NSString *stage=result.stage==CV_FOCUS_SYSTEM?@"cannot read focused application":result.stage==CV_FOCUS_FRONT?@"cannot confirm foreground process":result.stage==CV_FOCUS_CHANGED?@"foreground changed during focus confirmation":@"no accessible focused window";
  return fail(error,[NSString stringWithFormat:@"%@ (native error %d; AX missing-value fallback %@)",stage,result.error,result.used_fallback?@"used":@"not used"]);
 }
 *pid=(pid_t)result.pid;*bounds=CGRectMake(result.bounds.x,result.bounds.y,result.bounds.width,result.bounds.height);return 1;
}
static BOOL sameBounds(CGRect a,CGRect b) {
 return fabs(a.origin.x-b.origin.x)<1&&fabs(a.origin.y-b.origin.y)<1&&fabs(a.size.width-b.size.width)<1&&fabs(a.size.height-b.size.height)<1;
}
char *cv_windows(int active) { @autoreleasepool {
 char *reason=NULL;if(!requireConsole(&reason)){char *out=json(@{@"error":@(reason)});free(reason);return out;}
 pid_t front=0;CGRect focused=CGRectZero;BOOL useAX=active&&AXIsProcessTrusted();
 if(useAX){if(!focusedWindow(&front,&focused,&reason)){char *out=json(@{@"error":@(reason)});free(reason);return out;}}
 else if(active)front=NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier;
 NSArray *windows=active?windowInfo():allWindowInfo();if(!windows)return json(@{@"error":@"WindowServer is unavailable"});
 NSMutableArray *out=[NSMutableArray array];
 for(NSDictionary *w in windows) {if([w[(__bridge NSString*)kCGWindowLayer] intValue]!=0)continue;pid_t pid=[w[(__bridge NSString*)kCGWindowOwnerPID] intValue];if(active&&pid!=front)continue;
 CGRect b; if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)w[(__bridge NSString*)kCGWindowBounds],&b)||b.size.width<=0||b.size.height<=0)continue;
 if(useAX&&!sameBounds(b,focused))continue;
 char path[PROC_PIDPATHINFO_MAXSIZE]={0};proc_pidpath(pid,path,sizeof(path));NSString *title=w[(__bridge NSString*)kCGWindowName]?:@"";
 NSMutableDictionary *item=[@{@"native_id":w[(__bridge NSString*)kCGWindowNumber],@"pid":@(pid),@"title":title,@"process":@(path),@"bounds":rect(b)} mutableCopy];
 // CGWindow.h defines an absent kCGWindowIsOnscreen as offscreen.
 item[@"on_screen"]=cv_json_bool([w[(__bridge NSString*)kCGWindowIsOnscreen] boolValue]);
 [out addObject:item];if(active&&!useAX)break;
 }return json(out);
}}
static NSData *png(CGImageRef image) {
 if(!image)return nil;NSMutableData *out=[NSMutableData data];CGImageDestinationRef dest=CGImageDestinationCreateWithData((__bridge CFMutableDataRef)out,CFSTR("public.png"),1,NULL);if(!dest)return nil;
 CGImageDestinationAddImage(dest,image,NULL);BOOL ok=CGImageDestinationFinalize(dest);CFRelease(dest);return ok?out:nil;
}
// Async callbacks own this state even after timeout; no stack pointers escape.
@interface CVShot : NSObject
@property(nonatomic,strong) NSData *data;
@property(nonatomic,strong) NSString *error;
@end
@implementation CVShot
@end
int cv_capture(uint32_t display, int timeout_ms, void **bytes, size_t *length, char **error) { @autoreleasepool {
 if(!requireConsole(error))return 0;
 if(!CGPreflightScreenCaptureAccess())return fail(error,@"Screen Recording permission missing: run --request-permissions, grant it in System Settings > Privacy & Security > Screen Recording, then restart the server/host app");
 CGRect bounds=CGDisplayBounds(display);if(CGRectIsEmpty(bounds))return fail(error,@"display is no longer available");
 if(bounds.size.width*bounds.size.height>32000000)return fail(error,@"display exceeds pixel budget");
 NSData *data=nil;
Class manager=NSClassFromString(@"SCScreenshotManager");SEL captureSelector=NSSelectorFromString(@"captureImageWithFilter:configuration:completionHandler:");
 if(manager&&[manager respondsToSelector:captureSelector]) {
 CVShot *state=[CVShot new];dispatch_semaphore_t done=dispatch_semaphore_create(0);
 [SCShareableContent getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content,NSError *err){
 if(err){state.error=err.localizedDescription;dispatch_semaphore_signal(done);return;}
 SCDisplay *target=nil;for(SCDisplay *item in content.displays)if(item.displayID==display){target=item;break;}
 if(!target){state.error=@"display not available in ScreenCaptureKit";dispatch_semaphore_signal(done);return;}
 SCContentFilter *filter=[[SCContentFilter alloc]initWithDisplay:target excludingWindows:@[]];SCStreamConfiguration *cfg=[SCStreamConfiguration new];cfg.width=(size_t)bounds.size.width;cfg.height=(size_t)bounds.size.height;cfg.showsCursor=NO;
 typedef void(*CaptureFunction)(id,SEL,SCContentFilter*,SCStreamConfiguration*,void(^)(CGImageRef,NSError*));
 ((CaptureFunction)objc_msgSend)(manager,captureSelector,filter,cfg,^(CGImageRef image,NSError *captureError){state.error=captureError.localizedDescription;state.data=png(image);dispatch_semaphore_signal(done);});
 }];
 if(dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,(int64_t)timeout_ms*NSEC_PER_MSEC))!=0)return fail(error,@"ScreenCaptureKit timed out");
 if(state.error)return fail(error,state.error);data=state.data;
 } else {
 // macOS 13 fallback; resolved dynamically to keep builds compatible with SDKs
 // where the old declaration is unavailable. SCK is selected at runtime on 14+, even when built with SDK 13.
 typedef CGImageRef(*LegacyShot)(CGRect,CGWindowListOption,CGWindowID,CGWindowImageOption);
 LegacyShot capture=(LegacyShot)dlsym(RTLD_DEFAULT,"CGWindowListCreateImage");
 if(!capture)return fail(error,@"legacy capture unavailable; rebuild with macOS SDK 14+ for ScreenCaptureKit");
 CGImageRef image=capture(bounds,kCGWindowListOptionOnScreenOnly,kCGNullWindowID,kCGWindowImageNominalResolution);data=png(image);if(image)CGImageRelease(image);
 }
 if(!data)return fail(error,@"screen capture returned no image; verify Screen Recording permission and SDK/runtime support");
 if(!requireConsole(error))return 0;
 if(data.length>134217728)return fail(error,@"encoded image exceeds memory budget");
 *bytes=malloc(data.length);if(!*bytes)return fail(error,@"out of memory");memcpy(*bytes,data.bytes,data.length);*length=data.length;return 1;
}}
// Defined below with the structural AX helpers; no field values are read.
static id uiAttribute(AXUIElementRef element,CFStringRef name);
static CGRect uiBounds(AXUIElementRef element);
static cv_focus_rect scrollRect(CGRect r){return (cv_focus_rect){r.origin.x,r.origin.y,r.size.width,r.size.height};}
static BOOL scrollAXHit(CGPoint point,pid_t front,cv_focus_rect *bounds) {
 AXUIElementRef system=AXUIElementCreateSystemWide(),hit=NULL;
 if(!system)return NO;
 AXUIElementSetMessagingTimeout(system,0.15);
 AXError status=AXUIElementCopyElementAtPosition(system,(float)point.x,(float)point.y,&hit);CFRelease(system);
 if(status!=kAXErrorSuccess||!hit){if(hit)CFRelease(hit);return NO;}
 pid_t owner=0;if(AXUIElementGetPid(hit,&owner)!=kAXErrorSuccess||owner!=front){CFRelease(hit);return NO;}
 id window=uiAttribute(hit,kAXWindowAttribute);
 AXUIElementRef target=NULL;
 if(window&&CFGetTypeID((__bridge CFTypeRef)window)==AXUIElementGetTypeID())target=(__bridge AXUIElementRef)window;
 else if([uiAttribute(hit,kAXRoleAttribute) isEqual:(__bridge NSString*)kAXWindowRole])target=hit;
 BOOL valid=target&&AXUIElementGetPid(target,&owner)==kAXErrorSuccess&&owner==front;
 if(valid){*bounds=scrollRect(uiBounds(target));valid=cv_scroll_rect_valid(*bounds);}
 CFRelease(hit);return valid;
}
int cv_event(int kind,double x,double y,int delta,int horizontal,uint32_t expectedWindow,int expectedPID,char **error) { @autoreleasepool {
 if(!requireAccessibility(error,1))return 0;
 CGEventRef event=NULL;
 if(kind==1)event=CGEventCreateMouseEvent(NULL,kCGEventMouseMoved,CGPointMake(x,y),kCGMouseButtonLeft);
 else if(kind==2){
 CGEventRef current=CGEventCreate(NULL);if(!current)return fail(error,@"cannot read cursor position");CGPoint point=CGEventGetLocation(current);CFRelease(current);
 pid_t front=0;CGRect focused; if(!focusedWindow(&front,&focused,error))return 0;
 if(!expectedWindow||front!=expectedPID)return fail(error,@"scroll foreground differs from the requested window process; stop input");
 cv_focus_rect focus=scrollRect(focused),hitBounds={0};BOOL quartzVerified=NO;
 if(!cv_scroll_target(front,focus,point.x,point.y,1,0,hitBounds))return fail(error,[NSString stringWithFormat:@"scroll cursor (%.0f,%.0f) is outside focused window (%.0f,%.0f,%.0f,%.0f); move to the task area first",point.x,point.y,focused.origin.x,focused.origin.y,focused.size.width,focused.size.height]);
 NSArray *windows=windowInfo();if(!windows)return fail(error,@"WindowServer unavailable during scroll target check");
 BOOL targetPresent=NO;for(NSDictionary *w in windows){CGRect targetBounds;
 if([w[(__bridge NSString*)kCGWindowNumber] unsignedIntValue]==expectedWindow&&[w[(__bridge NSString*)kCGWindowOwnerPID] intValue]==front&&CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)w[(__bridge NSString*)kCGWindowBounds],&targetBounds)&&cv_scroll_same_window(focus,scrollRect(targetBounds))){targetPresent=YES;break;}}
 if(!targetPresent)return fail(error,@"requested scroll window is not the focused window; stop input");
 for(NSDictionary *w in windows){CGRect bounds;if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)w[(__bridge NSString*)kCGWindowBounds],&bounds))continue;if(!CGRectContainsPoint(bounds,point))continue;
 quartzVerified=[w[(__bridge NSString*)kCGWindowNumber] unsignedIntValue]==expectedWindow&&[w[(__bridge NSString*)kCGWindowLayer]intValue]==0&&[w[(__bridge NSString*)kCGWindowOwnerPID]intValue]==front&&cv_scroll_same_window(focus,scrollRect(bounds));break;}
 int hitPID=0;if(!quartzVerified&&scrollAXHit(point,front,&hitBounds))hitPID=front;
 if(!cv_scroll_target(front,focus,point.x,point.y,quartzVerified,hitPID,hitBounds))return fail(error,@"scroll target not confirmed: Quartz and AX hit-test could not establish the focused window; stop input");
 pid_t checked=0;CGRect checkedBounds;if(!focusedWindow(&checked,&checkedBounds,error))return 0;
 if(checked!=front||!cv_scroll_same_window(focus,scrollRect(checkedBounds)))return fail(error,@"foreground window changed during scroll target check; stop input");
 current=CGEventCreate(NULL);if(!current)return fail(error,@"cannot recheck cursor position");CGPoint checkedPoint=CGEventGetLocation(current);CFRelease(current);
 if(checkedPoint.x!=point.x||checkedPoint.y!=point.y)return fail(error,@"cursor moved during scroll target check; stop input");
 if(!requireAccessibility(error,1))return 0;
 event=CGEventCreateScrollWheelEvent(NULL,kCGScrollEventUnitLine,2,horizontal?0:delta,horizontal?-delta:0);
 if(event)CGEventSetLocation(event,point);
 }
 if(!event)return fail(error,@"could not create input event");CGEventPost(kCGHIDEventTap,event);CFRelease(event);return 1;
}}
static NSDictionary *focusTarget(uint32_t window,pid_t expected) {
 for(NSDictionary *w in allWindowInfo())if([w[(__bridge NSString*)kCGWindowNumber] unsignedIntValue]==window&&(!expected||[w[(__bridge NSString*)kCGWindowOwnerPID]intValue]==expected))return w;
 return nil;
}
static BOOL activateFocusApp(NSRunningApplication *running,char **error){
 if(!running){fail(error,@"target application is no longer running");return NO;}
 if(running.hidden&&![running unhide]){fail(error,@"macOS denied unhiding the target application");return NO;}
 if(![running activateWithOptions:NSApplicationActivateIgnoringOtherApps]){fail(error,@"macOS denied application activation");return NO;}
 return YES;
}
static AXUIElementRef matchFocusWindow(AXUIElementRef app,NSDictionary *target,cv_window_match *selection,AXError *readError,int *windowCount,int *failedBounds,NSTimeInterval deadline){
 *selection=(cv_window_match){-1,0,0};*windowCount=0;*failedBounds=0;
 CGRect targetRect;if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)target[(__bridge NSString*)kCGWindowBounds],&targetRect)){*readError=kAXErrorIllegalArgument;return NULL;}
 if(NSProcessInfo.processInfo.systemUptime>=deadline){*readError=kAXErrorCannotComplete;return NULL;}
 AXUIElementSetMessagingTimeout(app,(float)MIN(0.5,MAX(0.01,deadline-NSProcessInfo.processInfo.systemUptime)));
 CFTypeRef result=NULL;*readError=AXUIElementCopyAttributeValue(app,kAXWindowsAttribute,&result);
 if(*readError!=kAXErrorSuccess||!result){if(result)CFRelease(result);if(*readError==kAXErrorSuccess)*readError=kAXErrorNoValue;return NULL;}
 if(CFGetTypeID(result)!=CFArrayGetTypeID()){CFRelease(result);*readError=kAXErrorIllegalArgument;return NULL;}
 NSArray *list=(__bridge NSArray*)result;*windowCount=(int)list.count;
 if(list.count>512){CFRelease(result);*readError=kAXErrorIllegalArgument;return NULL;}
 cv_window_candidate *items=calloc(MAX((NSUInteger)1,list.count),sizeof(*items));
 if(!items){CFRelease(result);*readError=kAXErrorFailure;return NULL;}
 NSString *title=target[(__bridge NSString*)kCGWindowName];
 for(NSUInteger i=0;i<list.count;i++){
  if(NSProcessInfo.processInfo.systemUptime>=deadline){free(items);CFRelease(result);*readError=kAXErrorCannotComplete;return NULL;}
  AXUIElementRef w=(__bridge AXUIElementRef)list[i];if(CFGetTypeID(w)!=AXUIElementGetTypeID()){(*failedBounds)++;continue;}
  AXUIElementSetMessagingTimeout(w,(float)MIN(0.5,MAX(0.01,deadline-NSProcessInfo.processInfo.systemUptime)));
  CFTypeRef pos=NULL,size=NULL,name=NULL;CGPoint point;CGSize extent;
  BOOL ok=AXUIElementCopyAttributeValue(w,kAXPositionAttribute,&pos)==kAXErrorSuccess&&AXUIElementCopyAttributeValue(w,kAXSizeAttribute,&size)==kAXErrorSuccess&&pos&&size&&CFGetTypeID(pos)==AXValueGetTypeID()&&CFGetTypeID(size)==AXValueGetTypeID()&&AXValueGetValue(pos,kAXValueCGPointType,&point)&&AXValueGetValue(size,kAXValueCGSizeType,&extent);
  if(ok){items[i].valid=1;items[i].bounds=(cv_focus_rect){point.x,point.y,extent.width,extent.height};}else(*failedBounds)++;
  if(ok&&sameBounds(CGRectMake(point.x,point.y,extent.width,extent.height),targetRect)&&title.length&&AXUIElementCopyAttributeValue(w,kAXTitleAttribute,&name)==kAXErrorSuccess&&name&&CFGetTypeID(name)==CFStringGetTypeID())items[i].title_match=[title isEqualToString:(__bridge NSString*)name];
  if(pos)CFRelease(pos);if(size)CFRelease(size);if(name)CFRelease(name);
 }
 if(NSProcessInfo.processInfo.systemUptime>=deadline){free(items);CFRelease(result);*readError=kAXErrorCannotComplete;return NULL;}
 *selection=cv_select_window((cv_focus_rect){targetRect.origin.x,targetRect.origin.y,targetRect.size.width,targetRect.size.height},items,list.count);free(items);
 AXUIElementRef selected=NULL;if(selection->index>=0){selected=(__bridge AXUIElementRef)list[selection->index];CFRetain(selected);}CFRelease(result);return selected;
}
int cv_focus(uint32_t window,char **error) { @autoreleasepool {
 if(!requireAccessibility(error,0))return 0;
 NSDictionary *target=focusTarget(window,0);if(!target)return fail(error,@"window is no longer available");
 pid_t pid=[target[(__bridge NSString*)kCGWindowOwnerPID]intValue];
 AXUIElementRef app=AXUIElementCreateApplication(pid);AXUIElementSetMessagingTimeout(app,0.5);
 NSTimeInterval deadline=NSProcessInfo.processInfo.systemUptime+2.0;
 cv_window_match selection;AXError readError;int windows=0,failedBounds=0;BOOL activated=NO;
 AXUIElementRef match=matchFocusWindow(app,target,&selection,&readError,&windows,&failedBounds,deadline);
 // A background Electron app may not expose its AX windows until activated.
 // Activate at most once, never click/type, then refresh the exact Quartz ID.
 int front=0;BOOL background=freshFrontPID(NULL,&front)==0&&front!=pid;
 if(!match&&background&&NSProcessInfo.processInfo.systemUptime<deadline&&selection.bounds_matches==0&&(readError==kAXErrorSuccess||readError==kAXErrorNoValue||readError==kAXErrorCannotComplete)){
  if(!activateFocusApp([NSRunningApplication runningApplicationWithProcessIdentifier:pid],error)){CFRelease(app);return 0;}activated=YES;
  // Activation is asynchronous. Poll only read-side AX exposure within the budget;
  // never post a second activation or relax matching after a timeout.
  for(int attempt=0;attempt<10&&NSProcessInfo.processInfo.systemUptime<deadline;attempt++){
   if(attempt)usleep(50000);
   target=focusTarget(window,pid);if(!target){CFRelease(app);return fail(error,@"target window changed after application activation");}
   match=matchFocusWindow(app,target,&selection,&readError,&windows,&failedBounds,deadline);
   if(match||selection.bounds_matches>0||(readError!=kAXErrorSuccess&&readError!=kAXErrorNoValue&&readError!=kAXErrorCannotComplete))break;
  }
 }
 if(!match){CFRelease(app);return fail(error,[NSString stringWithFormat:@"cannot uniquely match window to Accessibility element; refusing focus (AX windows=%d, bounds matches=%d, title matches=%d, unreadable bounds=%d, AX error=%d, activation attempted=%@)",windows,selection.bounds_matches,selection.title_matches,failedBounds,readError,activated?@"yes":@"no"]);}
 CFTypeRef minimized=NULL;AXError e;
 if(AXUIElementCopyAttributeValue(match,kAXMinimizedAttribute,&minimized)==kAXErrorSuccess&&minimized&&CFGetTypeID(minimized)==CFBooleanGetTypeID()&&CFBooleanGetValue((CFBooleanRef)minimized)){
  e=AXUIElementSetAttributeValue(match,kAXMinimizedAttribute,kCFBooleanFalse);
  if(e!=kAXErrorSuccess){CFRelease(minimized);CFRelease(match);CFRelease(app);return fail(error,@"macOS denied restoring the minimized window");}
 }
 if(minimized)CFRelease(minimized);
 if(!activated&&!activateFocusApp([NSRunningApplication runningApplicationWithProcessIdentifier:pid],error)){CFRelease(match);CFRelease(app);return 0;}
 e=AXUIElementPerformAction(match,kAXRaiseAction);
 if(e==kAXErrorSuccess){AXUIElementSetAttributeValue(match,kAXMainAttribute,kCFBooleanTrue);AXUIElementSetAttributeValue(app,kAXFocusedWindowAttribute,match);}
 CFRelease(match);CFRelease(app);
 if(e!=kAXErrorSuccess)return fail(error,@"macOS denied AXRaise");return 1;
}}

int cv_button(int button,int phase,double x,double y,int count,char **error) { @autoreleasepool {
 if(phase!=0&&!requireAccessibility(error,1))return 0;
 CGEventType types[3][3]={{kCGEventLeftMouseUp,kCGEventLeftMouseDown,kCGEventLeftMouseDragged},{kCGEventRightMouseUp,kCGEventRightMouseDown,kCGEventRightMouseDragged},{kCGEventOtherMouseUp,kCGEventOtherMouseDown,kCGEventOtherMouseDragged}};
 if(button<0||button>2||phase<0||phase>2)return fail(error,@"invalid mouse event");
 CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source)return fail(error,@"cannot create event source");
 CGEventRef event=CGEventCreateMouseEvent(source,types[button][phase],CGPointMake(x,y),(CGMouseButton)button);CFRelease(source);if(!event)return fail(error,@"cannot create mouse event");
 CGEventSetFlags(event,0);CGEventSetIntegerValueField(event,kCGMouseEventClickState,count);CGEventPost(kCGHIDEventTap,event);CFRelease(event);return 1;
}}
int cv_key(uint16_t code,int down,uint64_t flags,char **error) { @autoreleasepool {
 if(down&&!requireAccessibility(error,1))return 0;
 CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source)return fail(error,@"cannot create event source");
 CGEventRef event=CGEventCreateKeyboardEvent(source,code,down!=0);CFRelease(source);if(!event)return fail(error,@"cannot create keyboard event");CGEventSetFlags(event,(CGEventFlags)flags);CGEventPost(kCGHIDEventTap,event);CFRelease(event);return 1;
}}
int cv_unicode(const uint16_t *units,int length,int down,char **error) { @autoreleasepool {
 if(down&&!requireAccessibility(error,1))return 0;
 if(length<1||length>2)return fail(error,@"invalid Unicode packet");CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source)return fail(error,@"cannot create event source");
 CGEventRef event=CGEventCreateKeyboardEvent(source,0,down!=0);CFRelease(source);if(!event)return fail(error,@"cannot create Unicode event");CGEventSetFlags(event,0);CGEventKeyboardSetUnicodeString(event,length,units);CGEventPost(kCGHIDEventTap,event);CFRelease(event);return 1;
}}

int cv_launch(const char *process,const char *url,char **error) { @autoreleasepool {
 if(!requireAccessibility(error,0))return 0;
 NSString *executable=[NSString stringWithUTF8String:process];
 if(!executable||![executable isAbsolutePath]||![executable isEqualToString:executable.stringByStandardizingPath])return fail(error,@"invalid application executable path");
 NSString *macos=executable.stringByDeletingLastPathComponent;
 NSString *contents=macos.stringByDeletingLastPathComponent;
 NSString *bundlePath=contents.stringByDeletingLastPathComponent;
 if(![macos.lastPathComponent isEqualToString:@"MacOS"]||![contents.lastPathComponent isEqualToString:@"Contents"]||![bundlePath.pathExtension isEqualToString:@"app"])return fail(error,@"expected .app/Contents/MacOS executable");
 NSBundle *bundle=[NSBundle bundleWithPath:bundlePath];
 if(!bundle||![[bundle objectForInfoDictionaryKey:@"CFBundlePackageType"] isEqualToString:@"APPL"]||![bundle.executablePath isEqualToString:executable]||![NSFileManager.defaultManager isExecutableFileAtPath:executable])return fail(error,@"path is not the main executable of an installed application bundle");
 // Validate both the exact supplied path and the bundle's main executable.
 // Launch Services receives only the bundle URL, never a shell or argv.
 NSURL *application=[NSURL fileURLWithPath:bundlePath isDirectory:YES];
 NSString *rawURL=[NSString stringWithUTF8String:url];NSURL *target=nil;
 if(!rawURL)return fail(error,@"invalid URL encoding");
 if(rawURL.length){target=[NSURL URLWithString:rawURL];if(!target||(![target.scheme isEqualToString:@"http"]&&![target.scheme isEqualToString:@"https"])||!target.host.length||target.user||target.password)return fail(error,@"only http/https URLs without credentials are allowed");}
 if(!requireAccessibility(error,0))return 0;
 NSError *launchError=nil;NSRunningApplication *running=nil;
 if(target)running=[NSWorkspace.sharedWorkspace openURLs:@[target] withApplicationAtURL:application options:NSWorkspaceLaunchDefault configuration:@{} error:&launchError];
 else running=[NSWorkspace.sharedWorkspace launchApplicationAtURL:application options:NSWorkspaceLaunchDefault configuration:@{} error:&launchError];
 if(!running)return fail(error,[NSString stringWithFormat:@"Launch Services rejected application: %@",launchError.localizedDescription?:@"unknown error"]);
 if(![running activateWithOptions:NSApplicationActivateIgnoringOtherApps])return fail(error,@"application launched but macOS denied activation");
 return 1;
}}


char *cv_cursor(void) { @autoreleasepool {
 char *reason=NULL;if(!requireConsole(&reason)){char *out=json(@{@"error":@(reason)});free(reason);return out;}
 CGEventRef event=CGEventCreate(NULL);if(!event)return json(@{@"error":@"cannot read cursor position"});
 CGPoint point=CGEventGetLocation(event);CFRelease(event);
 return json(@{@"x":@((int)floor(point.x)),@"y":@((int)floor(point.y))});
}}

static NSMutableDictionary *uiReferences;
static NSTimeInterval uiExpires;
// Structural UI operations never inspect existing field values during discovery.
static id uiAttribute(AXUIElementRef element,CFStringRef name){
 CFTypeRef value=NULL;AXUIElementSetMessagingTimeout(element,0.05);
 if(AXUIElementCopyAttributeValue(element,name,&value)!=kAXErrorSuccess){if(value)CFRelease(value);return nil;}
 return CFBridgingRelease(value);
}
static NSString *uiString(id value){if(![value isKindOfClass:NSString.class])return @"";NSString *text=value;return text.length>512?[text substringToIndex:512]:text;}
static CGRect uiBounds(AXUIElementRef element){
 id position=uiAttribute(element,kAXPositionAttribute),size=uiAttribute(element,kAXSizeAttribute);CGPoint p;CGSize s;
 if(!position||!size||CFGetTypeID((__bridge CFTypeRef)position)!=AXValueGetTypeID()||CFGetTypeID((__bridge CFTypeRef)size)!=AXValueGetTypeID()||!AXValueGetValue((__bridge AXValueRef)position,kAXValueCGPointType,&p)||!AXValueGetValue((__bridge AXValueRef)size,kAXValueCGSizeType,&s))return CGRectZero;
 return CGRectMake(p.x,p.y,s.width,s.height);
}
static AXUIElementRef uiWindow(uint32_t window,char **error){
 if(!requireAccessibility(error,0))return NULL;
 pid_t pid=0;CGRect bounds=CGRectZero;if(!focusedWindow(&pid,&bounds,error))return NULL;
 BOOL matched=NO;for(NSDictionary *info in windowInfo()){if([info[(__bridge NSString*)kCGWindowNumber] unsignedIntValue]!=window)continue;CGRect candidate;if(CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)info[(__bridge NSString*)kCGWindowBounds],&candidate)&&[info[(__bridge NSString*)kCGWindowOwnerPID] intValue]==pid&&sameBounds(candidate,bounds))matched=YES;}
 if(!matched){fail(error,@"UI window is not the requested foreground window");return NULL;}
 AXUIElementRef app=AXUIElementCreateApplication(pid);id focused=uiAttribute(app,kAXFocusedWindowAttribute);CFRelease(app);
 if(!focused||CFGetTypeID((__bridge CFTypeRef)focused)!=AXUIElementGetTypeID()||!sameBounds(uiBounds((__bridge AXUIElementRef)focused),bounds)){fail(error,@"focused UI window changed");return NULL;}
 return (AXUIElementRef)CFRetain((__bridge CFTypeRef)focused);
}
static NSDictionary *uiField(AXUIElementRef element,NSArray *path){
 NSString *role=uiString(uiAttribute(element,kAXRoleAttribute));
 if(![role isEqualToString:@"AXTextField"]&&![role isEqualToString:@"AXTextArea"]&&![role isEqualToString:@"AXComboBox"]&&![role isEqualToString:@"AXSecureTextField"])return nil;
 NSString *subrole=uiString(uiAttribute(element,kAXSubroleAttribute));BOOL secure=[subrole isEqualToString:@"AXSecureTextField"]||[role isEqualToString:@"AXSecureTextField"];
 NSString *label=uiString(uiAttribute(element,kAXTitleAttribute));if(!label.length)label=uiString(uiAttribute(element,kAXDescriptionAttribute));
 if(!label.length){id labelElement=uiAttribute(element,kAXTitleUIElementAttribute);if(labelElement&&CFGetTypeID((__bridge CFTypeRef)labelElement)==AXUIElementGetTypeID())label=uiString(uiAttribute((__bridge AXUIElementRef)labelElement,kAXTitleAttribute));}
 NSString *placeholder=uiString(uiAttribute(element,CFSTR("AXPlaceholderValue")));if(!label.length)label=placeholder;
 id enabled=uiAttribute(element,kAXEnabledAttribute);BOOL enabledFlag=enabled&&CFGetTypeID((__bridge CFTypeRef)enabled)==CFBooleanGetTypeID()&&CFBooleanGetValue((__bridge CFBooleanRef)enabled);
 Boolean settable=false;AXError status=AXUIElementIsAttributeSettable(element,kAXValueAttribute,&settable);
 return @{@"role":role,@"label":label,@"placeholder":placeholder,@"bounds":rect(uiBounds(element)),@"enabled":cv_json_bool(enabledFlag),@"editable":cv_json_bool(status==kAXErrorSuccess&&settable&&!secure),@"secure":cv_json_bool(secure),@"path":path};
}
static id uiControlAttribute(AXUIElementRef element,CFStringRef key,BOOL *incomplete){
 CFTypeRef value=NULL;AXError status=AXUIElementCopyAttributeValue(element,key,&value);
 if(status!=kAXErrorSuccess&&status!=kAXErrorNoValue&&status!=kAXErrorAttributeUnsupported)*incomplete=YES;
 if(status!=kAXErrorSuccess){if(value)CFRelease(value);return nil;}return CFBridgingRelease(value);
}
static NSDictionary *uiControl(AXUIElementRef element,BOOL *incomplete) {
 NSString *role=uiString(uiControlAttribute(element,kAXRoleAttribute,incomplete));
 if(![@[@"AXButton",@"AXPopUpButton",@"AXCheckBox",@"AXRadioButton",@"AXMenuItem"] containsObject:role])return nil;
 NSString *label=uiString(uiControlAttribute(element,kAXTitleAttribute,incomplete));if(!label.length)label=uiString(uiControlAttribute(element,kAXDescriptionAttribute,incomplete));
 id enabled=uiControlAttribute(element,kAXEnabledAttribute,incomplete);id known=NSNull.null;
 if(enabled&&CFGetTypeID((__bridge CFTypeRef)enabled)==CFBooleanGetTypeID())known=cv_json_bool(CFBooleanGetValue((__bridge CFBooleanRef)enabled));
 return @{@"role":role,@"label":label,@"bounds":rect(uiBounds(element)),@"enabled":known};
}
static char *uiRead(uint32_t window,int max_nodes,int timeout_ms,BOOL controls){@autoreleasepool{
 char *error=NULL;AXUIElementRef root=uiWindow(window,&error);if(!root){char *out=json(@{@"error":error?@(error):@"UI unavailable"});free(error);return out;}
 NSTimeInterval deadline=NSProcessInfo.processInfo.systemUptime+MIN(2000,MAX(1,timeout_ms))/1000.0;
 NSMutableArray *pending=[NSMutableArray arrayWithObject:@{@"element":(__bridge id)root,@"path":@[]}];CFRelease(root);
 NSMutableArray *fields=[NSMutableArray array];NSMutableDictionary *references=[NSMutableDictionary dictionary];int visited=0;BOOL truncated=NO;
 while(pending.count&&visited<max_nodes&&NSProcessInfo.processInfo.systemUptime<deadline){
  NSDictionary *item=pending.lastObject;[pending removeLastObject];AXUIElementRef element=(__bridge AXUIElementRef)item[@"element"];NSArray *path=item[@"path"];visited++;
  NSDictionary *field=controls?uiControl(element,&truncated):uiField(element,path);if(field){NSMutableDictionary *entry=[field mutableCopy];if(!controls){NSString *identity=NSUUID.UUID.UUIDString;entry[@"identity"]=identity;references[identity]=(__bridge id)element;}[fields addObject:entry];}
  CFIndex count=0;AXError childrenStatus=AXUIElementGetAttributeValueCount(element,kAXChildrenAttribute,&count);if(childrenStatus!=kAXErrorSuccess){if(controls&&childrenStatus!=kAXErrorNoValue&&childrenStatus!=kAXErrorAttributeUnsupported)truncated=YES;continue;}if(count<1)continue;
  if(path.count>=24){truncated=YES;continue;}
  CFIndex remaining=MAX(0,max_nodes-visited-(int)pending.count);CFIndex take=MIN(count,remaining);if(take<count)truncated=YES;if(take<=0)continue;
  CFArrayRef children=NULL;if(AXUIElementCopyAttributeValues(element,kAXChildrenAttribute,0,take,&children)!=kAXErrorSuccess||!children){if(controls)truncated=YES;if(children)CFRelease(children);continue;}
  for(CFIndex i=CFArrayGetCount(children);i>0;i--){CFTypeRef child=CFArrayGetValueAtIndex(children,i-1);if(CFGetTypeID(child)!=AXUIElementGetTypeID())continue;[pending addObject:@{@"element":(__bridge id)child,@"path":[path arrayByAddingObject:@(i-1)]}];}CFRelease(children);
 }
 if(pending.count)truncated=YES;if(!controls){@synchronized(NSProcessInfo.processInfo){uiReferences=references;uiExpires=NSProcessInfo.processInfo.systemUptime+60;}}return json(@{controls?@"controls":@"elements":fields,@"visited":@(visited),@"truncated":cv_json_bool(truncated)});
}}
char *cv_ui_snapshot(uint32_t window,int max_nodes,int timeout_ms){return uiRead(window,max_nodes,timeout_ms,NO);}
char *cv_ui_controls(uint32_t window,int max_nodes,int timeout_ms){return uiRead(window,max_nodes,timeout_ms,YES);}
void *cv_ui_cancel_new(void){_Atomic int *flag=calloc(1,sizeof(*flag));if(flag)atomic_init(flag,0);return flag;}
void cv_ui_cancel(void *cancel){if(cancel)atomic_store_explicit((_Atomic int*)cancel,1,memory_order_release);}
static BOOL uiCancelled(void *cancel){return !cancel||atomic_load_explicit((_Atomic int*)cancel,memory_order_acquire)!=0;}
int cv_ui_fill(uint32_t window,const char *expected_json,const char *text,int timeout_ms,void *cancel,char **error){@autoreleasepool{
 if(uiCancelled(cancel))return fail(error,@"UI write cancelled");
 NSDictionary *expected=[NSJSONSerialization JSONObjectWithData:[@(expected_json) dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
 if(![expected isKindOfClass:NSDictionary.class])return fail(error,@"invalid UI target");NSArray *path=expected[@"path"];
 if(![path isKindOfClass:NSArray.class]||path.count>24||path.count==0)return fail(error,@"invalid UI path");
 AXUIElementRef root=uiWindow(window,error);if(!root)return 0;id rootObject=CFBridgingRelease(root);id current=rootObject;
 NSTimeInterval deadline=NSProcessInfo.processInfo.systemUptime+MIN(2000,MAX(1,timeout_ms))/1000.0;
 for(id step in path){if(uiCancelled(cancel))return fail(error,@"UI write cancelled");if(NSProcessInfo.processInfo.systemUptime>=deadline)return fail(error,@"UI lookup timed out before writing");if(![step isKindOfClass:NSNumber.class]||[step integerValue]<0||[step integerValue]>2000)return fail(error,@"invalid UI path index");
  CFArrayRef children=NULL;AXUIElementRef element=(__bridge AXUIElementRef)current;AXUIElementSetMessagingTimeout(element,0.05);
  if(AXUIElementCopyAttributeValues(element,kAXChildrenAttribute,[step integerValue],1,&children)!=kAXErrorSuccess||!children)return fail(error,@"stale UI path; refresh ui_snapshot");
  NSArray *list=CFBridgingRelease(children);if(list.count!=1||CFGetTypeID((__bridge CFTypeRef)list[0])!=AXUIElementGetTypeID())return fail(error,@"stale UI element");current=list[0];
 }
 id original=nil;@synchronized(NSProcessInfo.processInfo){if(NSProcessInfo.processInfo.systemUptime<uiExpires&&[expected[@"identity"] isKindOfClass:NSString.class])original=uiReferences[expected[@"identity"]];}
 if(!original||!CFEqual((__bridge CFTypeRef)original,(__bridge CFTypeRef)current))return fail(error,@"UI element replaced; refresh ui_snapshot");
 AXUIElementRef target=(__bridge AXUIElementRef)current;NSDictionary *fresh=uiField(target,path);
 if(!fresh||![fresh[@"role"] isEqual:expected[@"role"]]||![fresh[@"label"] isEqual:expected[@"label"]]||![fresh[@"placeholder"] isEqual:expected[@"placeholder"]?:@""]||![fresh[@"bounds"] isEqual:expected[@"bounds"]]||![fresh[@"enabled"] boolValue]||![fresh[@"editable"] boolValue]||[fresh[@"secure"] boolValue])return fail(error,@"field changed, disabled, protected or read-only; refresh ui_snapshot");
 CGRect bounds=uiBounds(target),windowBounds=uiBounds((__bridge AXUIElementRef)rootObject);if(CGRectIsEmpty(bounds)||!CGRectContainsRect(windowBounds,bounds))return fail(error,@"field is not within the foreground window");
 AXUIElementRef confirmed=uiWindow(window,error);if(!confirmed)return 0;BOOL same=CFEqual(confirmed,(__bridge CFTypeRef)rootObject);CFRelease(confirmed);if(!same)return fail(error,@"foreground changed before field write");
 if(NSProcessInfo.processInfo.systemUptime>=deadline)return fail(error,@"UI lookup timed out before writing");
 AXUIElementSetMessagingTimeout(target,(float)MIN(0.5,MAX(0.01,deadline-NSProcessInfo.processInfo.systemUptime)));
 if(uiCancelled(cancel))return fail(error,@"UI write cancelled before mutation");
 AXError result=AXUIElementSetAttributeValue(target,kAXValueAttribute,(__bridge CFStringRef)@(text));if(result!=kAXErrorSuccess)return fail(error,[NSString stringWithFormat:@"field write not confirmed (AX error %d); inspect before retrying",result]);
 id value=uiAttribute(target,kAXValueAttribute);if(![value isKindOfClass:NSString.class]||![value isEqualToString:@(text)])return fail(error,@"field readback differs; inspect before retrying");
 return 1;
}}

#ifndef CV_FOCUS_RESOLVER_H
#define CV_FOCUS_RESOLVER_H
#include <math.h>
// A bounded read-only fallback for AX's missing system-wide focused-app value.
// No cached NSWorkspace identity, window-list guessing, activation or input.
typedef struct { double x,y,width,height; } cv_focus_rect;
typedef struct {
 int (*system_pid)(void *,int *);
 int (*front_pid)(void *,int *);
 int (*app_bounds)(void *,int,cv_focus_rect *);
} cv_focus_readers;
typedef struct {int pid,error,stage,used_fallback;cv_focus_rect bounds;} cv_focus_result;
enum {CV_FOCUS_SYSTEM=1,CV_FOCUS_FRONT=2,CV_FOCUS_WINDOW=3,CV_FOCUS_CHANGED=4,CV_FOCUS_BOUNDS=5};
static inline int cv_resolve_focus(const cv_focus_readers *read,void *ctx,cv_focus_result *out){
 *out=(cv_focus_result){0};
 out->stage=CV_FOCUS_SYSTEM;out->error=read->system_pid(ctx,&out->pid);
 if(out->error==-25212){
  out->used_fallback=1;out->stage=CV_FOCUS_FRONT;
  out->error=read->front_pid(ctx,&out->pid);
 }
 if(out->error||out->pid<=0)return 0;
 out->stage=CV_FOCUS_WINDOW;out->error=read->app_bounds(ctx,out->pid,&out->bounds);
 if(out->error)return 0;
 if(out->used_fallback){
  int confirmed=0;out->stage=CV_FOCUS_CHANGED;
  out->error=read->front_pid(ctx,&confirmed);
  if(out->error||confirmed!=out->pid)return 0;
 }
 cv_focus_rect b=out->bounds;out->stage=CV_FOCUS_BOUNDS;
 if(!isfinite(b.x)||!isfinite(b.y)||!isfinite(b.width)||!isfinite(b.height)||b.width<=0||b.height<=0)return 0;
 out->stage=0;return 1;
}
#endif

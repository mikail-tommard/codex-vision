#ifndef CV_SCROLL_TARGET_H
#define CV_SCROLL_TARGET_H
#include "focus_resolver.h"

static inline int cv_scroll_rect_valid(cv_focus_rect r) {
 return isfinite(r.x)&&isfinite(r.y)&&isfinite(r.width)&&isfinite(r.height)&&r.width>0&&r.height>0;
}
static inline int cv_scroll_same_window(cv_focus_rect a,cv_focus_rect b) {
 return cv_scroll_rect_valid(a)&&cv_scroll_rect_valid(b)&&fabs(a.x-b.x)<1&&fabs(a.y-b.y)<1&&fabs(a.width-b.width)<1&&fabs(a.height-b.height)<1;
}
// Quartz bounding boxes can include noninteractive overlays. Only a native AX
// hit in the focused window may resolve that ambiguity; never skip layers blindly.
static inline int cv_scroll_target(int front,cv_focus_rect focused,double x,double y,int quartz_verified,int hit_pid,cv_focus_rect hit_window) {
 if(front<=0||!cv_scroll_rect_valid(focused)||!isfinite(x)||!isfinite(y)||x<focused.x||y<focused.y||x>=focused.x+focused.width||y>=focused.y+focused.height)return 0;
 return quartz_verified||(hit_pid==front&&cv_scroll_same_window(focused,hit_window));
}
#endif

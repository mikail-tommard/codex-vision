#ifndef CV_WINDOW_MATCH_H
#define CV_WINDOW_MATCH_H
#include <math.h>
#include <stddef.h>
#include "focus_resolver.h"
typedef struct {cv_focus_rect bounds;int valid,title_match;} cv_window_candidate;
typedef struct {int index,bounds_matches,title_matches;} cv_window_match;
static inline cv_window_match cv_select_window(cv_focus_rect target,const cv_window_candidate *items,size_t count){
 cv_window_match out={-1,0,0};int geometry=-1,title=-1;
 for(size_t i=0;i<count;i++){
  cv_focus_rect b=items[i].bounds;
  if(!items[i].valid||!isfinite(b.x)||!isfinite(b.y)||!isfinite(b.width)||!isfinite(b.height)||b.width<=0||b.height<=0)continue;
  if(fabs(b.x-target.x)>=1||fabs(b.y-target.y)>=1||fabs(b.width-target.width)>=1||fabs(b.height-target.height)>=1)continue;
  out.bounds_matches++;geometry=(int)i;
  if(items[i].title_match){out.title_matches++;title=(int)i;}
 }
 if(out.bounds_matches==1)out.index=geometry;
 else if(out.bounds_matches>1&&out.title_matches==1)out.index=title;
 return out;
}
#endif

#ifndef CV_JSON_BOOL_H
#define CV_JSON_BOOL_H
#import <Foundation/Foundation.h>
// Objective-C boxing of C Boolean/logical expressions can produce numeric
// NSNumber values. Always emit the CFBoolean-backed JSON true/false objects.
static inline NSNumber *cv_json_bool(int value){return value?@YES:@NO;}
#endif

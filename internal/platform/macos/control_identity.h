#import <Foundation/Foundation.h>
#import <CoreFoundation/CoreFoundation.h>

// CF dictionary keys use native CFEqual/CFHash, not labels, coordinates or tree
// positions. Retention is bounded and IDs are scoped to the current AX root and
// document attribute. No document URL or native pointer is returned to MCP.
@interface CVControlIdentityCache : NSObject {
 CFMutableDictionaryRef identities;
 NSMutableDictionary *objects;
 id root;
 NSString *document;
 NSTimeInterval expires;
}
@property(nonatomic, readonly) NSString *contextID;
@property(nonatomic, readonly) NSString *contextKind;
- (void)beginRoot:(id)current document:(NSString *)currentDocument at:(NSTimeInterval)now;
- (NSString *)identityFor:(id)element;
- (id)elementForIdentity:(NSString *)identity;
@end

@implementation CVControlIdentityCache
- (instancetype)init {
 self=[super init];
 if(self){identities=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);objects=[NSMutableDictionary dictionary];}
 return self;
}
- (void)dealloc {if(identities)CFRelease(identities);}
- (void)beginRoot:(id)current document:(NSString *)currentDocument at:(NSTimeInterval)now {
 BOOL sameDocument=(document==nil&&currentDocument==nil)||[document isEqualToString:currentDocument];
 if(!root||!current||!CFEqual((__bridge CFTypeRef)root,(__bridge CFTypeRef)current)||!sameDocument||now>=expires||CFDictionaryGetCount(identities)>=2000){
  CFDictionaryRemoveAllValues(identities);[objects removeAllObjects];
  root=current;document=[currentDocument copy];expires=now+60;
  _contextID=NSUUID.UUID.UUIDString;
  _contextKind=currentDocument?@"native_window_document":@"native_window";
 }
}
- (NSString *)identityFor:(id)element {
 if(!element)return nil;
 NSString *existing=(__bridge NSString *)CFDictionaryGetValue(identities,(__bridge CFTypeRef)element);
 if(existing)return existing;
 if(CFDictionaryGetCount(identities)>=2000)return nil;
 NSString *token=NSUUID.UUID.UUIDString;
 CFDictionarySetValue(identities,(__bridge CFTypeRef)element,(__bridge CFTypeRef)token);objects[token]=element;
 return token;
}
- (id)elementForIdentity:(NSString *)identity {return [identity isKindOfClass:NSString.class]?objects[identity]:nil;}
@end

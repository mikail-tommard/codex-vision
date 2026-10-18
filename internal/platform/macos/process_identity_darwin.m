//go:build darwin && cgo
#import <Foundation/Foundation.h>
#import <Security/Security.h>
#include <libproc.h>
#include <string.h>
#include <stdlib.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <errno.h>
#include "bridge.h"

static BOOL processPathMatches(pid_t pid, NSString *raw) {
 char current[PROC_PIDPATHINFO_MAXSIZE]={0};
 return proc_pidpath(pid,current,sizeof(current))>0&&[raw isEqualToString:@(current)];
}
static BOOL unchangedFile(struct stat a,struct stat b) {
 return a.st_dev==b.st_dev&&a.st_ino==b.st_ino&&a.st_size==b.st_size&&a.st_mtimespec.tv_sec==b.st_mtimespec.tv_sec&&a.st_mtimespec.tv_nsec==b.st_mtimespec.tv_nsec&&a.st_ctimespec.tv_sec==b.st_ctimespec.tv_sec&&a.st_ctimespec.tv_nsec==b.st_ctimespec.tv_nsec;
}
// Exact-path policy already trusts the configured executable. An OS clone of
// those same executable bytes inherits that identity without relying on XPC
// signing services. Compare bounded regular files through stable descriptors.
int cv_executable_copy_matches(const char *raw,const char *expected) {
 int left=open(raw,O_RDONLY|O_CLOEXEC|O_NOFOLLOW|O_NONBLOCK),right=open(expected,O_RDONLY|O_CLOEXEC|O_NOFOLLOW|O_NONBLOCK);
 if(left<0||right<0){if(left>=0)close(left);if(right>=0)close(right);return 0;}
 struct stat a={0},b={0},afterA={0},afterB={0};BOOL same=NO;
 if(fstat(left,&a)==0&&fstat(right,&b)==0&&S_ISREG(a.st_mode)&&S_ISREG(b.st_mode)&&a.st_size>0&&a.st_size==b.st_size&&a.st_size<=512LL*1024*1024){
  unsigned char first[65536],second[65536];off_t offset=0;same=YES;
  while(offset<a.st_size){size_t count=(size_t)MIN((off_t)sizeof(first),a.st_size-offset);ssize_t n,m;
   do{n=pread(left,first,count,offset);}while(n<0&&errno==EINTR);
   do{m=pread(right,second,count,offset);}while(m<0&&errno==EINTR);
   if(n!=(ssize_t)count||m!=n||memcmp(first,second,count)!=0){same=NO;break;}offset+=n;
  }
  if(fstat(left,&afterA)!=0||fstat(right,&afterB)!=0||!unchangedFile(a,afterA)||!unchangedFile(b,afterB))same=NO;
 }
 close(left);close(right);return same?1:0;
}
static void identityError(char **error, NSString *message, OSStatus status) {
 if(error)*error=strdup([NSString stringWithFormat:@"%@ (Security OSStatus %d)",message,(int)status].UTF8String);
}

// Read-only identity check. It neither grants TCC access nor posts input.
// The observed PID/path must match an exact executable copy or a dynamically
// validated signing identity.
int cv_process_matches(int pid,const char *raw,const char *expected,char **error) { @autoreleasepool {
 NSString *actual=[NSString stringWithUTF8String:raw];
 NSString *executable=[NSString stringWithUTF8String:expected];
 if(pid<=0||!actual||!executable||![actual isAbsolutePath]||![executable isAbsolutePath]||[actual rangeOfString:@".code_sign_clone/"].location==NSNotFound||![actual.lastPathComponent isEqualToString:executable.lastPathComponent])return 0;
 if(![actual isEqualToString:actual.stringByStandardizingPath]||![executable isEqualToString:executable.stringByStandardizingPath])return 0;
 if(!processPathMatches((pid_t)pid,actual)){identityError(error,@"running executable path changed or is unreadable",errSecCSNoSuchCode);return 0;}
 NSString *macos=executable.stringByDeletingLastPathComponent;
 NSString *contents=macos.stringByDeletingLastPathComponent;
 NSString *bundlePath=contents.stringByDeletingLastPathComponent;
 if(![macos.lastPathComponent isEqualToString:@"MacOS"]||![contents.lastPathComponent isEqualToString:@"Contents"]||![bundlePath.pathExtension isEqualToString:@"app"])return 0;
 NSBundle *bundle=[NSBundle bundleWithPath:bundlePath];
 if(!bundle||![bundle.executablePath isEqualToString:executable]||![[bundle objectForInfoDictionaryKey:@"CFBundlePackageType"] isEqualToString:@"APPL"])return 0;
 // Recognize an exact executable copy first. Different versions must pass
 // the independent signed-publisher/application check below.
 if(cv_executable_copy_matches(raw,expected)&&processPathMatches((pid_t)pid,actual))return 1;
 SecStaticCodeRef installed=NULL;SecCodeRef running=NULL;
 SecRequirementRef anchor=NULL,requirement=NULL;
 CFDictionaryRef installedInfo=NULL,runningInfo=NULL;
 OSStatus status=errSecSuccess;int matched=0;
 do {
  status=SecStaticCodeCreateWithPath((__bridge CFURLRef)[NSURL fileURLWithPath:bundlePath],kSecCSDefaultFlags,&installed);
  if(status!=errSecSuccess){identityError(error,@"cannot read installed application signature",status);break;}
  status=SecRequirementCreateWithString(CFSTR("anchor apple generic"),kSecCSDefaultFlags,&anchor);
  if(status!=errSecSuccess){identityError(error,@"cannot create signing trust requirement",status);break;}
  // Validate the reference signing identity, not every resource in a large
  // Chrome bundle. The running process is validated dynamically below.
  status=SecStaticCodeCheckValidity(installed,kSecCSBasicValidateOnly,anchor);
  if(status!=errSecSuccess){identityError(error,@"installed application signing identity is not trusted",status);break;}
  status=SecCodeCopyDesignatedRequirement(installed,kSecCSDefaultFlags,&requirement);
  if(status!=errSecSuccess||!requirement){identityError(error,@"installed application has no designated requirement",status);break;}
  NSDictionary *attributes=@{(__bridge NSString*)kSecGuestAttributePid:@(pid)};
  status=SecCodeCopyGuestWithAttributes(NULL,(__bridge CFDictionaryRef)attributes,kSecCSDefaultFlags,&running);
  if(status!=errSecSuccess){identityError(error,@"cannot inspect running application code",status);break;}
  status=SecCodeCheckValidity(running,kSecCSDefaultFlags,anchor);
  if(status==errSecSuccess)status=SecCodeCheckValidity(running,kSecCSDefaultFlags,requirement);
  if(status!=errSecSuccess){identityError(error,@"running clone signature does not match the installed application",status);break;}
  status=SecCodeCopySigningInformation(installed,kSecCSSigningInformation,&installedInfo);
  if(status==errSecSuccess)status=SecCodeCopySigningInformation(running,kSecCSSigningInformation,&runningInfo);
  if(status!=errSecSuccess||!installedInfo||!runningInfo){identityError(error,@"cannot read validated application signing identifiers",status);break;}
  NSDictionary *reference=(__bridge NSDictionary*)installedInfo,*live=(__bridge NSDictionary*)runningInfo;
  NSString *team=reference[(__bridge NSString*)kSecCodeInfoTeamIdentifier],*identifier=reference[(__bridge NSString*)kSecCodeInfoIdentifier];
  if(![team isKindOfClass:NSString.class]||!team.length||![identifier isKindOfClass:NSString.class]||!identifier.length||![team isEqual:live[(__bridge NSString*)kSecCodeInfoTeamIdentifier]]||![identifier isEqual:live[(__bridge NSString*)kSecCodeInfoIdentifier]]){identityError(error,@"running clone signing team or application identifier differs",errSecCSReqFailed);break;}
  if(!processPathMatches((pid_t)pid,actual)){identityError(error,@"running executable changed during identity verification",errSecCSNoSuchCode);break;}
  matched=1;
 }while(0);
 if(installedInfo)CFRelease(installedInfo);if(runningInfo)CFRelease(runningInfo);
 if(anchor)CFRelease(anchor);if(requirement)CFRelease(requirement);
 if(installed)CFRelease(installed);if(running)CFRelease(running);
 return matched;
}}

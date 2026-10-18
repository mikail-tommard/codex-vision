#include <stddef.h>
#include <stdint.h>
char *cv_monitors(void);
char *cv_windows(int active);
int cv_capture(uint32_t display, int timeout_ms, void **bytes, size_t *length, char **error);
int cv_event(int kind, double x, double y, int delta, int horizontal, uint32_t window, int pid, char **error);
int cv_focus(uint32_t window, char **error);

int cv_button(int button,int phase,double x,double y,int count,char **error);
int cv_key(uint16_t code,int down,uint64_t flags,char **error);
int cv_unicode(const uint16_t *units,int length,int down,char **error);

// All flags are normalized 0/1; tri-state session fields use -1 for unknown.
typedef struct {
 int accessibility_permission;
 int post_event_permission;
 int screen_capture_permission;
 int session_available;
 int on_console;
 int login_done;
 int screen_locked;
 int lock_indicator_present;
 int session_uid_known;
 unsigned int session_uid;
 unsigned int effective_uid;
} cv_permission_state;
void cv_read_permissions(cv_permission_state *state);
void cv_request_accessibility(void);
char *cv_process_identity(void);

void cv_request_screen_capture(void);

const char *cv_permission_failure(const cv_permission_state *state,int checks);

int cv_launch(const char *process, const char *url, char **error);

char *cv_cursor(void);

int cv_process_matches(int pid, const char *raw, const char *expected, char **error);

int cv_executable_copy_matches(const char *raw, const char *expected);
char *cv_ui_snapshot(uint32_t window,int max_nodes,int timeout_ms);
int cv_ui_fill(uint32_t window,const char *expected,const char *text,int timeout_ms,void *cancel,char **error);

void *cv_ui_cancel_new(void);
void cv_ui_cancel(void *cancel);

char *cv_ui_controls(uint32_t window,int max_nodes,int timeout_ms);

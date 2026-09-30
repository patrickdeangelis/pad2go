/* Minimal EventToken.h for MinGW-w64, which doesn't ship it; WebView2.h
   only needs the EventRegistrationToken type (as in the Windows SDK). */
#ifndef __eventtoken_h__
#define __eventtoken_h__
typedef struct EventRegistrationToken {
  __int64 value;
} EventRegistrationToken;
#endif

	opt		d+				;Debug symbols

; Example code to demonstrate playback.
; The definition DELTA_PACK is used to switch the build between
; the normal compressed file version, and the simple delta-pack.

main:
	; Allocate memory
	movea.l	a7,a5
	movea.l	4(a5),a5			;base page
	move.l	12(a5),d0			;text size
	add.l	20(a5),d0			;data size
	add.l	28(a5),d0			;bss size
	addi.l	#$0500,d0			;stack size
	move.l	d0,d1				;copy total size
	add.l	a5,d1				;plus base size
	and.l	#-2,d1				;align
	movea.l	d1,a7				;new stack address
	move.l	d0,-(a7)			;size
	move.l	a5,-(a7)			;base of our prog
	clr.w	-(a7)				;dummy parameter
	move.w	#$4a,-(a7)			;mshrink()
	trap	#1				;call gemdos
	lea	12(a7),a7			;fix stack

	; Run core
	pea	modplay_loop
	move.w	#38,-(sp)
	trap	#14
	addq.l	#6,sp

	; Core:
	; Call func to set up player system
	; Install Timer C callback to play each frame
	; Start playback

	; (wait for keypress)

	; Remove interrupt
	; Exit

	clr.w	-(sp)				;pterm
	trap	#1				;call gemdos

modplay_loop:
	lea	player_state,a0
	lea	tune_data,a1
	lea	player_cache,a2
	bsr	ymp_player_init

	; Install timer C as test
	or.w	#$0700,sr			; disable interrupts
	move.l	$114.w,old_c
	move.l	#c_routine,$114.w		; install Timer C hook
	clr.b	$484.w
	move.w	#$2300,sr			; interrupts on

	; wait key
	move.w	#8,-(a7)
	trap	#1
	addq.l	#2,a7

	; Restore system
	or.w	#$0700,sr			; disable interrupts
	move.l	old_c,$114.w
	move.b	#7,$ffff8800.w			; mixer reg
	move.b	#%00111111,$ffff8802.w		; suppress all channels
	move.w	#$2300,sr			; interrupts on

	clr.w	-(a7)
	trap	#1

;-------------
c_routine:
	sub.w	#50,tccount			; Allow variable play speed (here TC=50)
	bpl.s	.skip
	add.w	#200,tccount
	move.w	$ffff8240.w,-(a7)
	move.w	#$700,$ffff8240.w

	; Do the playback
	movem.l	d0-a6,-(a7)
	lea	player_state,a0
	bsr	ymp_player_update
	movem.l	(a7)+,d0-a6

	move.w	(a7)+,$ffff8240.w
.skip:	move.l	old_c,-(sp)			; Jump to existing Timer C (OS interrupt)
	rts

tccount			dc.w	200		;timer C down counter
old_c:			ds.l	1

			ifd	DELTA_PACK
; ----------------- Delta pack variant -----------------

			include	"yd.s"
tune_data:		incbin	"example.yd"
			section	bss
player_cache		ds.b	1		; not needed
player_state:		ds.b	yd_size

			else
; ----------------- Fill compression variant -----------------
			; Player code
			include	"ymp.s"

; Our packed data file.
tune_data:		incbin	"example.ymp"
			even
tune_data_end:

			section	bss
; Data space for each copy of playback state
player_state		ds.b	ymp_size

; LZ cache for player. Size depends on the compressed file.
player_cache		ds.b	8192		; (or whatever size you need)
			endif

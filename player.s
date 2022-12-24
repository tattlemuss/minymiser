	opt		d+				;Debug symbols

main:
	; Allocate memory
	movea.l	a7,a5
	movea.l	4(a5),a5		;base page
	move.l	12(a5),d0		;text size
	add.l	20(a5),d0		;data size
	add.l	28(a5),d0		;bss size
	addi.l	#$0500,d0		;stack size
	move.l	d0,d1			;copy total size
	add.l	a5,d1			;plus base size
	and.l	#-2,d1			;align
	movea.l	d1,a7			;new stack address
	move.l	d0,-(a7)		;size
	move.l	a5,-(a7)		;base of our prog
	clr.w	-(a7)			;dummy parameter
	move.w	#$4a,-(a7)		;mshrink()
	trap	#1				;call gemdos
	lea	12(a7),a7		;fix stack

	; Run core
	pea	modplay_loop
	move.w	#38,-(sp)
	trap	#14
	addq.l	#6,sp

	; Core:
	; Call func to set up player system
	; Call func to set a specific module to play
	; Start playback

	; (wait for keypress)
	; Stop playback
	; Clear player system
	; Exit

	clr.l	-(sp)			;pterm
	trap	#1				;call gemdos

modplay_loop:

.waitstart:	
	;cmp.b 	#$2,$fffffc02.w
	;bne.s	.waitstart

	bsr	player_init

	; Install timer C as test
	or.w	#$0700,sr			;disable interrupts
	move.l	$114.w,old_c
	move.l	#c_routine,$114.w
	move.b	#%1000000,$17(a0)		; irq vector register (automat. eoi)

	clr.b	$484.w
	move.w	#$2300,sr			; interrupts on
.key:	
	cmp.b 	#$39,$fffffc02.w
	bne.s	.key

	; :TODO: restore old systems
	move.l	old_c,$114.w
	rts

;-------------
c_routine
	move.w	#$2500,sr			; Allow other MFP interrupts (ikbd, border) to run

	sub.w	#50,tccount
	bpl.s	.skip
	add.w	#200,tccount
	eor.w	#$ff,$ffff8240.w
	movem.l	d0-a6,-(a7)
	bsr	player_update
	movem.l	(a7)+,d0-a6
	eor.w	#$ff,$ffff8240.w
.skip
	move.l	old_c,-(sp)
	rts

tccount	dc.w	200			;timer C down counter... this is a bit severe!
old_c:	ds.l	1

; -----------------------------------------------------------------------
player_init:
	move.l	#player_data,player_ptr
	move.l	#player_cache,player_cache_pos
	rts

player_update:
	move.l	player_ptr(pc),a0
	moveq	#0,d0
	move.b	(a0)+,d0
	bne.s	.cached_frame

	; Copy position of next 14 bytes into cache
	move.l	player_cache_pos(pc),a1		;d1 = cache pos
	move.l	a0,a2				;a2 = YM data
	move.l	a0,(a1)+			;write into cache
	add.w	#14,a0				;skip note data

	cmp.l	#player_cache+255*4,a1
	bne.s	.no_wrap
	lea	player_cache(pc),a1
.no_wrap:
	move.l	a1,player_cache_pos
	bra.s	.player_ym_write
.cached_frame:
	; This value is in the range 1-255
	subq.w	#1,d0
	add.w	d0,d0
	add.w	d0,d0
	lea	player_cache(pc),a2
	move.l	(a2,d0.w),a2			;a2 = YM data from cache
.player_ym_write:
	move.l	a0,player_ptr

	; a2 is now the YM data
r	set	0
	rept	14
	move.b	#r,$ffff8800.w
	move.b	(a2)+,$ffff8802.w
r	set	r+1
	endr
	rts

player_ptr		ds.l	1
player_cache_pos:	ds.l	1		; offset into player_cache
player_cache:		ds.l	255


player_data:	incbin	sanxion.ymp



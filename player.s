	opt		d+				;Debug symbols

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
	; Call func to set a specific module to play
	; Start playback

	; (wait for keypress)
	; Stop playback
	; Clear player system
	; Exit

	clr.w	-(sp)				;pterm
	trap	#1				;call gemdos

modplay_loop:

.waitstart:
	;cmp.b 	#$2,$fffffc02.w
	;bne.s	.waitstart

	lea	player_data,a0
	bsr	ymp_player_init

	; Install timer C as test
	or.w	#$0700,sr			;disable interrupts
	move.l	$114.w,old_c
	move.l	#c_routine,$114.w
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

	sub.w	#50,tccount			; Allow variable play speed (here TC=50)
	bpl.s	.skip
	add.w	#200,tccount
	movem.l	d0-a6,-(a7)
	bsr	ymp_player_update
	move.w	#$777,$ffff8240.w
	movem.l	(a7)+,d0-a6
.skip:	move.l	old_c,-(sp)
	rts

tccount			dc.w	200		;timer C down counter
old_c:			ds.l	1


			include	"ymp.s"

; Our packed data file.
;player_data:		incbin	goexp/minimal.ymp
player_data:		incbin	goexp/test.ymp
			even
player_data_end: